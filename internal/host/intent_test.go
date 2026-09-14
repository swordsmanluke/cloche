package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIntentFixtureProject(t *testing.T) (dir, reqID string) {
	t.Helper()
	dir = t.TempDir()
	store := intent.NewStore(dir)
	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Never bump the major version unless explicitly told to.",
	})
	require.NoError(t, err)
	return dir, created.ID
}

func TestExecutor_AgentStep_SeedsAndInjectsIntentBlock(t *testing.T) {
	tmpDir, reqID := newIntentFixtureProject(t)
	outputDir := filepath.Join(tmpDir, "output")

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)

	captured, err := os.ReadFile(filepath.Join(tmpDir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "Standing project requirements")
	assert.Contains(t, string(captured), reqID)

	intentVal, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "intent")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, intentVal, reqID)

	idsVal, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "develop:implement:intent")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, reqID, idsVal)
}

func TestExecutor_AgentStep_NoIntentDir_NoKVSeeded(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
		},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(tmpDir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.NotContains(t, string(captured), "Standing project requirements")

	_, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "intent")
	require.NoError(t, err)
	assert.False(t, ok, "no intent dir should mean no KV seeding at all")
}

func TestExecutor_AgentStep_StepLevelIntentTrackingOff_SkipsInjection(t *testing.T) {
	tmpDir, reqID := newIntentFixtureProject(t)
	outputDir := filepath.Join(tmpDir, "output")
	_ = reqID

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":          "You are a coding assistant.",
			"agent_command":   mockAgent,
			"intent_tracking": "false",
		},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(tmpDir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.NotContains(t, string(captured), "Standing project requirements")

	_, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "intent")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestExecutor_AgentStep_WorkflowLevelIntentTrackingOff_SkipsInjection(t *testing.T) {
	tmpDir, _ := newIntentFixtureProject(t)
	outputDir := filepath.Join(tmpDir, "output")

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:        tmpDir,
		OutputDir:         outputDir,
		Store:             store,
		HostRunID:         "run1",
		TaskID:            "task1",
		AttemptID:         "att1",
		WorkflowName:      "develop",
		IntentTrackingOff: true,
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
		},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	_, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "intent")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestExecutor_AgentStep_ConfigTomlIntentOff_SkipsInjection(t *testing.T) {
	tmpDir, _ := newIntentFixtureProject(t)
	outputDir := filepath.Join(tmpDir, "output")

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".cloche", "config.toml"), []byte("[intent]\ninject = \"off\"\n"), 0644))

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
		},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	_, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "intent")
	require.NoError(t, err)
	assert.False(t, ok)
}
