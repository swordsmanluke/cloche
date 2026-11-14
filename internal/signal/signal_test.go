package signal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriority(t *testing.T) {
	assert.Equal(t, "0", Priority(SeverityUrgent))
	assert.Equal(t, "1", Priority(SeverityHigh))
	assert.Equal(t, "3", Priority(SeverityLow))
}

func TestTokenStore(t *testing.T) {
	dir := t.TempDir()
	s := NewTokenStore(filepath.Join(dir, "tokens"))
	proj := "/work/p"

	assert.False(t, s.Valid(proj, "anything"), "no token yet, nothing valid")
	assert.False(t, s.Valid(proj, ""))

	tok, err := s.Get(proj)
	require.NoError(t, err)
	assert.Contains(t, tok, "clo_sig_")
	again, _ := s.Get(proj)
	assert.Equal(t, tok, again, "Get is stable")
	assert.True(t, s.Valid(proj, tok))
	assert.False(t, s.Valid(proj, tok+"x"))
	assert.False(t, s.Valid(proj, tok[:len(tok)-1]))
	assert.False(t, s.Valid("/work/other", tok), "tokens are per project")

	// A second store on the same dir (the CLI) sees and can rotate it.
	cli := NewTokenStore(filepath.Join(dir, "tokens"))
	rotated, err := cli.Rotate(proj)
	require.NoError(t, err)
	assert.NotEqual(t, tok, rotated)
	assert.False(t, s.Valid(proj, tok))
	assert.True(t, s.Valid(proj, rotated))

	entries, _ := os.ReadDir(filepath.Join(dir, "tokens"))
	require.Len(t, entries, 1)
	info, _ := entries[0].Info()
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

type memStore struct {
	mu sync.Mutex
	n  int64
}

func (m *memStore) SaveSignal(context.Context, string, Signal) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	return m.n, nil
}

func TestDispatcher_CoalescesAndNotifiesRunningHandler(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan *InFlight, 1)
	var runs atomic.Int32
	var lastCount atomic.Int32
	d := &Dispatcher{Store: &memStore{}, Handler: func(_ context.Context, _ string, _ Signal, f *InFlight) error {
		runs.Add(1)
		f.OnAdd(func(c int, _ Signal) { lastCount.Store(int32(c)) })
		started <- f
		<-gate
		return nil
	}}
	sig := Signal{Source: "s", Key: "k", Severity: SeverityLow, Title: "t"}

	r, err := d.Submit(context.Background(), "/p", sig)
	require.NoError(t, err)
	assert.False(t, r.Coalesced)
	f := <-started

	for i := 0; i < 500; i++ {
		r, err := d.Submit(context.Background(), "/p", sig)
		require.NoError(t, err)
		require.True(t, r.Coalesced)
	}
	assert.Equal(t, 501, f.Count())
	assert.EqualValues(t, 501, lastCount.Load())

	// Same key in another project is independent.
	r, err = d.Submit(context.Background(), "/q", sig)
	require.NoError(t, err)
	assert.False(t, r.Coalesced)
	<-started

	close(gate)
	d.Wait()
	assert.EqualValues(t, 2, runs.Load())
}

func TestInFlight_OnAddCatchesUp(t *testing.T) {
	f := &InFlight{count: 1}
	f.add(Signal{Key: "a"})
	f.add(Signal{Key: "b"})
	var got int
	var latest string
	f.OnAdd(func(c int, s Signal) { got, latest = c, s.Key })
	assert.Equal(t, 3, got)
	assert.Equal(t, "b", latest)
}

func TestInFlight_ClosedRejectsJoin(t *testing.T) {
	f := &InFlight{count: 1}
	f.close()
	assert.False(t, f.add(Signal{}))
}

func TestJudgePrompt_RulesThenGuideThenIDsThenReporting(t *testing.T) {
	p := JudgePrompt("GUIDE-TEXT", []string{"Q1", "Q2"})
	order := []string{"Cite your sources", "GUIDE-TEXT", "- Q1", "- Q2", "CLOCHE_RESULT:{{ $result_nonce }}:success"}
	last := -1
	for _, s := range order {
		i := strings.Index(p, s)
		require.Greater(t, i, last, "%q out of order in:\n%s", s, p)
		last = i
	}
	assert.True(t, strings.HasSuffix(strings.TrimSpace(p), "aborts the run."), "reporting section must end the prompt")
}
