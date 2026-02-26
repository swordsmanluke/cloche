package agent_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/agent"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner_CaptureWiredToStatus(t *testing.T) {
	dir := t.TempDir()

	// Write user prompt under run ID
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "test-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test-run", "prompt.txt"), []byte("build a thing"), 0644))

	// Create a mock agent script that reads stdin and produces output
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'I built the thing'\n"), 0755))

	workflowContent := `workflow "capture-test" {
  step implement {
    agent_command = "` + mockAgent + `"
    prompt = "You are a coding assistant."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "capture.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
		RunID:        "test-run",
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	// Find the step_completed message for "implement"
	var found bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepCompleted && msg.StepName == "implement" {
			found = true
			assert.Equal(t, 1, msg.AttemptNumber, "attempt number should be 1")
			assert.Contains(t, msg.AgentOutput, "I built the thing")
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_ExecutesWorkflowFile(t *testing.T) {
	dir := t.TempDir()
	workflowContent := `workflow "simple-build" {
  step build {
    run = "echo building"
    results = [success, fail]
  }

  step test {
    run = "echo testing"
    results = [success, fail]
  }

  build:success -> test
  build:fail -> abort

  test:success -> done
  test:fail -> build
}`
	workflowPath := filepath.Join(dir, "simple.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	// Should have: build started, build completed, test started, test completed, run completed
	assert.GreaterOrEqual(t, len(msgs), 5)

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}
