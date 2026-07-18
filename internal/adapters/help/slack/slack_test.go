package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloche-dev/cloche/internal/domain"
)

// fakeBinder is an in-memory Binder for tests.
type fakeBinder struct {
	mu       sync.Mutex
	bindings map[string]string // key: channelName+"/"+threadID -> externalID
	byExtID  map[string]string // key: channelName+"/"+externalID -> threadID
}

func newFakeBinder() *fakeBinder {
	return &fakeBinder{bindings: map[string]string{}, byExtID: map[string]string{}}
}

func (b *fakeBinder) BindExternal(ctx context.Context, threadID, channelName, externalID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bindings[channelName+"/"+threadID] = externalID
	b.byExtID[channelName+"/"+externalID] = threadID
	return nil
}

func (b *fakeBinder) ResolveExternal(ctx context.Context, channelName, externalID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byExtID[channelName+"/"+externalID], nil
}

func (b *fakeBinder) GetExternalID(ctx context.Context, threadID, channelName string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bindings[channelName+"/"+threadID], nil
}

// fakeSink is an in-memory ports.ReplySink for tests.
type fakeSink struct {
	mu      sync.Mutex
	replies []replyCall
}

type replyCall struct {
	threadID, body, via string
}

func (s *fakeSink) Reply(ctx context.Context, threadID, body, via string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, replyCall{threadID, body, via})
	return nil
}

func (s *fakeSink) calls() []replyCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]replyCall, len(s.replies))
	copy(out, s.replies)
	return out
}

// newTestChannel builds a Channel wired to a fake Slack Web API server that
// records the last chat.postMessage request and returns a synthetic ts.
func newTestChannel(t *testing.T, cfg Config, binder Binder) (*Channel, *postRecorder) {
	t.Helper()
	rec := &postRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		rec.mu.Lock()
		rec.channel = r.FormValue("channel")
		rec.threadTS = r.FormValue("thread_ts")
		rec.blocksJSON = r.FormValue("blocks")
		rec.n++
		ts := fmt.Sprintf("%d.000000", 1000+rec.n)
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": r.FormValue("channel"), "ts": ts})
	}))
	t.Cleanup(server.Close)

	api := slack.New("xoxb-test-token", slack.OptionAPIURL(server.URL+"/"))
	return &Channel{cfg: cfg, binder: binder, api: api, sm: nil}, rec
}

type postRecorder struct {
	mu         sync.Mutex
	n          int
	channel    string
	threadTS   string
	blocksJSON string
}

func testThread(id, channel string) domain.HelpThread {
	return domain.HelpThread{
		ID: id, Channel: channel, Name: "schema-choice-1",
		TaskID: "task-a", RunID: "run-1", StepName: "plan", Title: "Which schema?",
	}
}

func TestNew_MissingTokens(t *testing.T) {
	_, err := New(Config{}, newFakeBinder())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SLACK_BOT_TOKEN")

	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	_, err = New(Config{}, newFakeBinder())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SLACK_APP_TOKEN")

	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	ch, err := New(Config{}, newFakeBinder())
	require.NoError(t, err)
	assert.Equal(t, "slack", ch.Name())
}

func TestPost_FirstMessageBindsThread(t *testing.T) {
	binder := newFakeBinder()
	ch, rec := newTestChannel(t, Config{Channel: "#cloche"}, binder)
	ctx := context.Background()

	thread := testThread("thread-1", "cloche")
	msg := domain.HelpMessage{Body: "Which schema should I use?", Options: []string{"Schema A", "Schema B"}}

	require.NoError(t, ch.Post(ctx, thread, msg))

	assert.Equal(t, "#cloche", rec.channel)
	assert.Empty(t, rec.threadTS, "first post should not set thread_ts")
	assert.Contains(t, rec.blocksJSON, "Which schema should I use?")
	assert.Contains(t, rec.blocksJSON, "Schema A")

	got, err := binder.GetExternalID(ctx, "thread-1", "slack")
	require.NoError(t, err)
	assert.Equal(t, "1001.000000", got)
}

func TestPost_FollowUpReusesBoundThread(t *testing.T) {
	binder := newFakeBinder()
	ch, rec := newTestChannel(t, Config{Channel: "#cloche"}, binder)
	ctx := context.Background()
	require.NoError(t, binder.BindExternal(ctx, "thread-1", "slack", "555.111"))

	thread := testThread("thread-1", "cloche")
	msg := domain.HelpMessage{Body: "Any migrations needed?"}
	require.NoError(t, ch.Post(ctx, thread, msg))

	assert.Equal(t, "555.111", rec.threadTS)

	// The binding is unchanged (still the original thread_ts).
	got, err := binder.GetExternalID(ctx, "thread-1", "slack")
	require.NoError(t, err)
	assert.Equal(t, "555.111", got)
}

func TestPost_ChannelMapOverride(t *testing.T) {
	binder := newFakeBinder()
	ch, rec := newTestChannel(t, Config{
		Channel:    "#cloche",
		ChannelMap: map[string]string{"mazd": "#mazd-cloche"},
	}, binder)
	ctx := context.Background()

	require.NoError(t, ch.Post(ctx, testThread("thread-1", "mazd"), domain.HelpMessage{Body: "Q?"}))
	assert.Equal(t, "#mazd-cloche", rec.channel)

	require.NoError(t, ch.Post(ctx, testThread("thread-2", "cloche"), domain.HelpMessage{Body: "Q?"}))
	assert.Equal(t, "#cloche", rec.channel)
}

func TestHandleMessage_ForwardsThreadReply(t *testing.T) {
	binder := newFakeBinder()
	ch := &Channel{cfg: Config{}, binder: binder, botUserID: "UBOT"}
	ctx := context.Background()
	require.NoError(t, binder.BindExternal(ctx, "thread-1", "slack", "100.000"))
	sink := &fakeSink{}

	ch.handleMessage(ctx, &slackevents.MessageEvent{
		User: "U123", Text: "Use schema B", ThreadTimeStamp: "100.000", TimeStamp: "101.000",
	}, sink)

	calls := sink.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "thread-1", calls[0].threadID)
	assert.Equal(t, "Use schema B", calls[0].body)
	assert.Equal(t, "slack", calls[0].via)
}

func TestHandleMessage_IgnoresBotMessages(t *testing.T) {
	binder := newFakeBinder()
	ch := &Channel{cfg: Config{}, binder: binder, botUserID: "UBOT"}
	ctx := context.Background()
	require.NoError(t, binder.BindExternal(ctx, "thread-1", "slack", "100.000"))
	sink := &fakeSink{}

	ch.handleMessage(ctx, &slackevents.MessageEvent{
		BotID: "B123", Text: "posted by cloche", ThreadTimeStamp: "100.000", TimeStamp: "101.000",
	}, sink)
	ch.handleMessage(ctx, &slackevents.MessageEvent{
		User: "UBOT", Text: "our own echo", ThreadTimeStamp: "100.000", TimeStamp: "101.000",
	}, sink)

	assert.Empty(t, sink.calls())
}

func TestHandleMessage_IgnoresNonThreadAndUnboundMessages(t *testing.T) {
	binder := newFakeBinder()
	ch := &Channel{cfg: Config{}, binder: binder}
	ctx := context.Background()
	sink := &fakeSink{}

	// No thread_ts at all: a top-level channel message, not a thread reply.
	ch.handleMessage(ctx, &slackevents.MessageEvent{User: "U1", Text: "hi", TimeStamp: "1.0"}, sink)
	// thread_ts == ts: this message *is* the thread root, not a reply into it.
	ch.handleMessage(ctx, &slackevents.MessageEvent{User: "U1", Text: "hi", ThreadTimeStamp: "1.0", TimeStamp: "1.0"}, sink)
	// Bound to a thread ts we don't recognize.
	ch.handleMessage(ctx, &slackevents.MessageEvent{User: "U1", Text: "hi", ThreadTimeStamp: "9.0", TimeStamp: "9.1"}, sink)

	assert.Empty(t, sink.calls())
}

func TestHandleBlockAction_ForwardsOptionText(t *testing.T) {
	binder := newFakeBinder()
	ch := &Channel{cfg: Config{}, binder: binder}
	ctx := context.Background()
	require.NoError(t, binder.BindExternal(ctx, "thread-1", "slack", "200.000"))
	sink := &fakeSink{}

	cb := slack.InteractionCallback{
		Type:      slack.InteractionTypeBlockActions,
		Container: slack.Container{MessageTs: "200.000"},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{{Value: "Schema B"}},
		},
	}
	ch.handleBlockAction(ctx, cb, sink)

	calls := sink.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "thread-1", calls[0].threadID)
	assert.Equal(t, "Schema B", calls[0].body)
}

func TestHandleBlockAction_PrefersThreadTsOverMessageTs(t *testing.T) {
	binder := newFakeBinder()
	ch := &Channel{cfg: Config{}, binder: binder}
	ctx := context.Background()
	require.NoError(t, binder.BindExternal(ctx, "thread-1", "slack", "300.000"))
	sink := &fakeSink{}

	cb := slack.InteractionCallback{
		Type:      slack.InteractionTypeBlockActions,
		Container: slack.Container{MessageTs: "300.999", ThreadTs: "300.000"},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{{Value: "Schema A"}},
		},
	}
	ch.handleBlockAction(ctx, cb, sink)

	calls := sink.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "thread-1", calls[0].threadID)
}

func TestFallbackText(t *testing.T) {
	thread := testThread("thread-1", "cloche")
	got := fallbackText(thread, domain.HelpMessage{Body: "Which schema should I use?"})
	assert.Equal(t, "Which schema?: Which schema should I use?", got)
}

// TestPost_Concurrent guards against data races between Post calls (the
// Router fans messages out to channels via goroutines).
func TestPost_Concurrent(t *testing.T) {
	binder := newFakeBinder()
	ch, _ := newTestChannel(t, Config{Channel: "#cloche"}, binder)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("thread-%d", i)
			_ = ch.Post(ctx, testThread(id, "cloche"), domain.HelpMessage{Body: "q"})
		}(i)
	}
	wg.Wait()
}
