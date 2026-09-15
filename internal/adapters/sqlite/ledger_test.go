package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func TestListContextKVForProject(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	require.NoError(t, s.SaveTask(ctx, &domain.Task{ID: "task1", ProjectDir: "/proj/a"}))
	require.NoError(t, s.SaveAttempt(ctx, &domain.Attempt{ID: "att1", TaskID: "task1"}))
	require.NoError(t, s.SaveTask(ctx, &domain.Task{ID: "task2", ProjectDir: "/proj/b"}))
	require.NoError(t, s.SaveAttempt(ctx, &domain.Attempt{ID: "att2", TaskID: "task2"}))

	require.NoError(t, s.SetContextKey(ctx, "task1", "att1", "run1", "develop:implement:prompt_file", ".cloche/prompts/implement.md"))
	require.NoError(t, s.SetContextKey(ctx, "task1", "att1", "run1", "develop:implement:prompt_rev", "abc123"))
	require.NoError(t, s.SetContextKey(ctx, "task2", "att2", "run2", "develop:implement:prompt_rev", "def456"))

	rows, err := s.ListContextKVForProject(ctx, "/proj/a")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, "task1", row.TaskID)
		assert.Equal(t, "att1", row.AttemptID)
	}

	rows, err = s.ListContextKVForProject(ctx, "/proj/b")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "def456", rows[0].Value)

	rows, err = s.ListContextKVForProject(ctx, "/proj/nonexistent")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestAttemptTokenTotals(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	require.NoError(t, s.SaveTask(ctx, &domain.Task{ID: "task1", ProjectDir: "/proj/a"}))
	require.NoError(t, s.SaveAttempt(ctx, &domain.Attempt{ID: "att1", TaskID: "task1"}))

	run := domain.NewRun("run1", "develop")
	run.ProjectDir = "/proj/a"
	run.AttemptID = "att1"
	run.TaskID = "task1"
	require.NoError(t, s.CreateRun(ctx, run))

	require.NoError(t, s.SaveCapture(ctx, "run1", &domain.StepExecution{
		StepName: "implement",
		Usage:    &domain.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}))
	require.NoError(t, s.SaveCapture(ctx, "run1", &domain.StepExecution{
		StepName: "fix",
		Usage:    &domain.TokenUsage{InputTokens: 10, OutputTokens: 5},
	}))

	// A second attempt/run in a different project must not contribute.
	require.NoError(t, s.SaveTask(ctx, &domain.Task{ID: "task2", ProjectDir: "/proj/b"}))
	require.NoError(t, s.SaveAttempt(ctx, &domain.Attempt{ID: "att2", TaskID: "task2"}))
	otherRun := domain.NewRun("run2", "develop")
	otherRun.ProjectDir = "/proj/b"
	otherRun.AttemptID = "att2"
	otherRun.TaskID = "task2"
	require.NoError(t, s.CreateRun(ctx, otherRun))
	require.NoError(t, s.SaveCapture(ctx, "run2", &domain.StepExecution{
		StepName: "implement",
		Usage:    &domain.TokenUsage{InputTokens: 999, OutputTokens: 999},
	}))

	totals, err := s.AttemptTokenTotals(ctx, "/proj/a")
	require.NoError(t, err)
	assert.Equal(t, int64(165), totals["att1"])
	_, ok := totals["att2"]
	assert.False(t, ok)
}
