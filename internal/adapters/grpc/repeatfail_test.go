package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/signal"
)

func TestRepeatFailureSignal(t *testing.T) {
	st := attention.FailureStreak{Count: 3, LatestRunID: "run-9", FailedStep: "test", Error: "boom"}

	sig, ok := repeatFailureSignal("T-1", st, 3)
	require.True(t, ok)
	assert.Equal(t, "T-1:test", sig.Key)
	assert.Equal(t, signal.SeverityHigh, sig.Severity)
	assert.Contains(t, sig.Title, "T-1")
	assert.Contains(t, sig.Title, "keeps failing")
	assert.Contains(t, sig.Evidence, "run-9")
	assert.NoError(t, sig.Validate())

	// Below the threshold a failure is not a signal; past it, it already signalled.
	st.Count = 2
	_, ok = repeatFailureSignal("T-1", st, 3)
	assert.False(t, ok)
	st.Count = 4
	_, ok = repeatFailureSignal("T-1", st, 3)
	assert.False(t, ok)

	// Unknown step still yields a valid, stable key.
	sig, ok = repeatFailureSignal("T-1", attention.FailureStreak{Count: 3}, 3)
	require.True(t, ok)
	assert.Equal(t, "T-1:unknown", sig.Key)
	assert.NoError(t, sig.Validate())
}

type fakeSubmitter struct{ got []signal.Signal }

func (f *fakeSubmitter) Submit(_ context.Context, _ string, s signal.Signal) (signal.Result, error) {
	f.got = append(f.got, s)
	return signal.Result{}, nil
}

func TestSignalRepeatFailure_NoSubmitterIsNoop(t *testing.T) {
	s := &ClocheServer{}
	s.signalRepeatFailure("/p", "T-1") // must not panic
	f := &fakeSubmitter{}
	s.SetSignalSubmitter(f)
	assert.Empty(t, f.got)
}
