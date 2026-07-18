package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mapKV is a minimal in-memory KVReader+KVWriter for park/resume tests.
type mapKV struct {
	data map[string]string
}

func newMapKV() *mapKV { return &mapKV{data: map[string]string{}} }

func (m *mapKV) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m.data[key]
	return v, ok, nil
}

func (m *mapKV) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func TestExtractSessionID_ClaudeInitEvent(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"sess-abc-123"}`)
	assert.Equal(t, "sess-abc-123", extractSessionID(line))
}

func TestExtractSessionID_NoSessionIDField(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	assert.Equal(t, "", extractSessionID(line))
}

func TestExtractSessionID_InvalidJSON(t *testing.T) {
	assert.Equal(t, "", extractSessionID([]byte(`not json`)))
}

func TestCaptureSessionID_PersistsToKVWriter(t *testing.T) {
	kv := newMapKV()
	a := New()
	a.KVWriter = kv

	output := []byte("{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"sess-xyz\"}\n{\"type\":\"assistant\"}\n")
	a.captureSessionID(context.Background(), "claude", output)

	v, found, err := kv.Get(context.Background(), kvAgentSessionID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "sess-xyz", v)
}

func TestCaptureSessionID_NoOpForNonClaudeCommand(t *testing.T) {
	kv := newMapKV()
	a := New()
	a.KVWriter = kv

	output := []byte(`{"type":"system","subtype":"init","session_id":"sess-xyz"}`)
	a.captureSessionID(context.Background(), "opencode", output)

	_, found, _ := kv.Get(context.Background(), kvAgentSessionID)
	assert.False(t, found, "session_id capture is claude-specific")
}

func TestCaptureSessionID_NoOpWithoutKVWriter(t *testing.T) {
	a := New() // KVWriter left nil
	output := []byte(`{"type":"system","subtype":"init","session_id":"sess-xyz"}`)
	// Must not panic.
	a.captureSessionID(context.Background(), "claude", output)
}

func TestBuildResumePrompt_NoSessionRecorded_FallsBackToRetry(t *testing.T) {
	a := New()
	a.KV = newMapKV()

	sessionID, prompt := a.buildResumePrompt(context.Background())
	assert.Equal(t, "", sessionID)
	assert.Equal(t, "retry", prompt)
}

func TestBuildResumePrompt_SessionButNoPendingAnswer_FallsBackToRetry(t *testing.T) {
	kv := newMapKV()
	kv.data[kvAgentSessionID] = "sess-abc"
	a := New()
	a.KV = kv

	sessionID, prompt := a.buildResumePrompt(context.Background())
	assert.Equal(t, "sess-abc", sessionID, "session id is still surfaced for --resume even without a pending answer")
	assert.Equal(t, "retry", prompt)
}

func TestBuildResumePrompt_ParkedAnswerPending_DeliversAsPrompt(t *testing.T) {
	kv := newMapKV()
	kv.data[kvAgentSessionID] = "sess-abc"
	kv.data[kvPendingAnswer] = "Use schema B"
	kv.data[kvPendingThreadID] = "thread-1"
	a := New()
	a.KV = kv

	sessionID, prompt := a.buildResumePrompt(context.Background())
	assert.Equal(t, "sess-abc", sessionID)
	assert.Equal(t, "Answer to your question in thread `thread-1`: Use schema B", prompt)
}

func TestBuildResumePrompt_NilKV_FallsBackToRetry(t *testing.T) {
	a := New() // KV left nil
	sessionID, prompt := a.buildResumePrompt(context.Background())
	assert.Equal(t, "", sessionID)
	assert.Equal(t, "retry", prompt)
}
