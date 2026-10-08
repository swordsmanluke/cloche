package host

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// inputRecordingStore is fakeStore plus an in-memory ports.StepInputStore,
// with one KV key in scope so the recorded script input lists it.
type inputRecordingStore struct {
	fakeStore
	inputs []*domain.StepInput
}

func (s *inputRecordingStore) ListContextKeys(_ context.Context, _, _, _ string) ([]string, error) {
	return []string{"child_branch"}, nil
}

func (s *inputRecordingStore) SaveStepInput(_ context.Context, in *domain.StepInput) error {
	n := 0
	for _, prev := range s.inputs {
		if prev.RunID == in.RunID && prev.StepName == in.StepName {
			n++
		}
	}
	in.Attempt = n + 1
	cp := *in
	s.inputs = append(s.inputs, &cp)
	return nil
}

func (s *inputRecordingStore) ListStepInputs(_ context.Context, runID, stepName string) ([]*domain.StepInput, error) {
	var out []*domain.StepInput
	for _, in := range s.inputs {
		if in.RunID == runID && in.StepName == stepName {
			out = append(out, in)
		}
	}
	return out, nil
}

// TestExecutor_RecordsScriptAndPollInputs verifies that host script and poll
// steps record their input against the host run before executing: the
// command, Cloche-provided env with values, other env by name only, and the
// KV keys in scope.
func TestExecutor_RecordsScriptAndPollInputs(t *testing.T) {
	store := &inputRecordingStore{fakeStore: fakeStore{runs: map[string]*domain.Run{}, kvData: map[string]string{}}}
	ctx := context.Background()

	tmpDir := t.TempDir()
	t.Setenv("SOME_SECRET", "hunter2")
	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  filepath.Join(tmpDir, "output"),
		Store:      store,
		HostRunID:  "att1-main",
		TaskID:     "task-1",
		AttemptID:  "att1",
	}

	_, err := executor.Execute(ctx, &domain.Step{
		Name: "greet", Type: domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	})
	require.NoError(t, err)

	inputs, err := store.ListStepInputs(ctx, "att1-main", "greet")
	require.NoError(t, err)
	require.Len(t, inputs, 1)
	in := inputs[0]
	require.Equal(t, domain.StepInputScript, in.Kind)
	require.Equal(t, 1, in.Attempt)
	for _, want := range []string{
		"command:\n  echo hello\n",
		"CLOCHE_RUN_ID=att1-main",
		"CLOCHE_TASK_ID=task-1",
		"CLOCHE_PROJECT_DIR=" + tmpDir,
		"kv keys:\n  child_branch\n",
	} {
		require.Contains(t, in.Content, want)
	}
	require.Contains(t, in.Content, "SOME_SECRET")
	require.NotContains(t, in.Content, "hunter2", "non-Cloche env values must not be recorded")

	// Running the same step again records a second attempt rather than
	// overwriting the first.
	_, err = executor.Execute(ctx, &domain.Step{
		Name: "greet", Type: domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo again"},
	})
	require.NoError(t, err)
	inputs, _ = store.ListStepInputs(ctx, "att1-main", "greet")
	require.Len(t, inputs, 2)
	require.True(t, strings.Contains(inputs[1].Content, "echo again") && inputs[1].Attempt == 2)

	// Poll step: interval and condition, recorded even though the poll itself
	// resolves immediately.
	_, err = executor.Execute(ctx, &domain.Step{
		Name: "wait", Type: domain.StepTypePoll,
		Results: []string{"ready", "timeout"},
		Config:  map[string]string{"interval": "1s", "poll": "echo CLOCHE_RESULT:ready"},
	})
	require.NoError(t, err)
	inputs, _ = store.ListStepInputs(ctx, "att1-main", "wait")
	require.Len(t, inputs, 1)
	require.Equal(t, domain.StepInputPoll, inputs[0].Kind)
	require.Equal(t, "interval: 1s\ncondition:\n  echo CLOCHE_RESULT:ready\n", inputs[0].Content)
}

// TestExecutor_NoStore_SkipsInputRecording verifies a store-less executor
// (tests, standalone use) runs steps normally with nothing to record into.
func TestExecutor_NoStore_SkipsInputRecording(t *testing.T) {
	tmpDir := t.TempDir()
	executor := &Executor{ProjectDir: tmpDir, OutputDir: filepath.Join(tmpDir, "output")}
	result, err := executor.Execute(context.Background(), &domain.Step{
		Name: "greet", Type: domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	})
	require.NoError(t, err)
	require.Equal(t, "success", result.Result)
}
