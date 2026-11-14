package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
)

func pollOutcome(t *testing.T, script string) (dra.PollOutcome, *ClocheServer, string) {
	t.Helper()
	srv, root := wakeServer(t)
	root = normalizeProjectDir(root)
	polls := dra.PollsDir(root, "auth")
	require.NoError(t, os.MkdirAll(polls, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(polls, "p.sh"), []byte("#!/bin/sh\n"+script), 0o755))
	spec, err := dra.ResolvePoll(root, "auth", dra.WaitRequest{Poll: "p"})
	require.NoError(t, err)
	return (&recordedPollRunner{s: srv}).Poll(context.Background(), spec), srv, root
}

// A poll is a recorded host run dra-wait-<name>; its marker, failure or
// silence maps to the outcome the registry acts on.
func TestRecordedPollRunner_OutcomesAndRunRecord(t *testing.T) {
	out, srv, root := pollOutcome(t, "echo hello; echo CLOCHE_RESULT:green\n")
	assert.Equal(t, "green", out.Result)
	assert.Contains(t, out.Output, "hello")
	assert.NotContains(t, out.Output, "CLOCHE_RESULT")

	runs, err := srv.store.ListRunsByProject(context.Background(), root, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "dra-wait-auth", runs[0].WorkflowName)
	assert.True(t, runs[0].IsHost)
	assert.Equal(t, domain.RunStateSucceeded, runs[0].State)

	out, _, _ = pollOutcome(t, "echo not yet\n")
	assert.Empty(t, out.Result)
	assert.False(t, out.Failed)

	out, _, _ = pollOutcome(t, "echo boom; exit 3\n")
	assert.True(t, out.Failed)
	assert.Contains(t, out.Output, "boom")

	// A marker decides, whatever the exit code.
	out, _, _ = pollOutcome(t, "echo CLOCHE_RESULT:red; exit 1\n")
	assert.Equal(t, "red", out.Result)
}
