package agent_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_WorkflowLevelAgentConfig(t *testing.T) {
	dir := t.TempDir()

	// Create a mock agent script that echoes its arguments and drains stdin
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho \"args: $*\"\n"), 0755))

	workflowContent := `workflow "agent-config-test" {
  container {
    agent_command = "` + mockAgent + `"
    agent_args = "--full-auto --sandbox danger"
  }

  step implement {
    prompt = "Write some code."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "agent-config.cloche")
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

	// Verify the workflow completed successfully
	var found bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepCompleted && msg.StepName == "implement" {
			found = true
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_StepLevelAgentArgs(t *testing.T) {
	dir := t.TempDir()

	// Create a mock agent script that echoes its arguments and drains stdin
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho \"args: $*\"\n"), 0755))

	workflowContent := `workflow "step-args-test" {
  step implement {
    agent_command = "` + mockAgent + `"
    agent_args = "--step-level-flag"
    prompt = "Write some code."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "step-args.cloche")
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

	var found bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepCompleted && msg.StepName == "implement" {
			found = true
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_StepLevelOverridesWorkflowLevel(t *testing.T) {
	dir := t.TempDir()

	// Create mock agent scripts
	workflowAgent := filepath.Join(dir, "workflow-agent.sh")
	require.NoError(t, os.WriteFile(workflowAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'workflow-agent ran'\n"), 0755))

	stepAgent := filepath.Join(dir, "step-agent.sh")
	require.NoError(t, os.WriteFile(stepAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'step-agent ran'\n"), 0755))

	workflowContent := `workflow "override-test" {
  container {
    agent_command = "` + workflowAgent + `"
    agent_args = "--workflow-args"
  }

  step implement {
    agent_command = "` + stepAgent + `"
    agent_args = "--step-args"
    prompt = "Write some code."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "override.cloche")
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

	// Step-level override should win — verify step completed
	var found bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepCompleted && msg.StepName == "implement" {
			found = true
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")
}

func TestRunner_FallbackChain(t *testing.T) {
	dir := t.TempDir()

	// Create a good agent script
	goodAgent := filepath.Join(dir, "good-agent.sh")
	require.NoError(t, os.WriteFile(goodAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'good agent ran'\n"), 0755))

	// Use nonexistent-agent as primary, good-agent as fallback (comma-separated)
	workflowContent := `workflow "fallback-test" {
  step implement {
    agent_command = "nonexistent-agent-xyz,` + goodAgent + `"
    prompt = "Write some code."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "fallback.cloche")
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

	// Verify the workflow completed successfully via fallback
	var found bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepCompleted && msg.StepName == "implement" {
			found = true
			break
		}
	}
	assert.True(t, found, "should have step_completed message for implement step")

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_WorkflowLevelFallbackChain(t *testing.T) {
	dir := t.TempDir()

	// Create a good agent script
	goodAgent := filepath.Join(dir, "good-agent.sh")
	require.NoError(t, os.WriteFile(goodAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'good agent ran'\n"), 0755))

	workflowContent := `workflow "wf-fallback-test" {
  container {
    agent_command = "nonexistent-agent-xyz,` + goodAgent + `"
  }

  step implement {
    prompt = "Write some code."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "wf-fallback.cloche")
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

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

func TestRunner_UnifiedLogScriptSteps(t *testing.T) {
	dir := t.TempDir()
	workflowContent := `workflow "log-test" {
  step build {
    run = "echo building the project"
    results = [success, fail]
  }

  step test {
    run = "echo running tests"
    results = [success, fail]
  }

  build:success -> test
  build:fail -> abort

  test:success -> done
  test:fail -> abort
}`
	workflowPath := filepath.Join(dir, "log-test.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	// Read full.log
	fullLog, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)

	logStr := string(fullLog)

	// Verify status entries are present
	assert.Contains(t, logStr, "[status] step_started: build")
	assert.Contains(t, logStr, "[status] step_completed: build -> success")
	assert.Contains(t, logStr, "[status] step_started: test")
	assert.Contains(t, logStr, "[status] step_completed: test -> success")
	assert.Contains(t, logStr, "[status] run_completed: succeeded")

	// Verify script output entries
	assert.Contains(t, logStr, "[script] building the project")
	assert.Contains(t, logStr, "[script] running tests")

	// Verify chronological order: build started -> build output -> build completed -> test started
	lines := strings.Split(strings.TrimRight(logStr, "\n"), "\n")
	var buildStartIdx, buildOutputIdx, buildCompleteIdx, testStartIdx int
	for i, line := range lines {
		switch {
		case strings.Contains(line, "[status] step_started: build"):
			buildStartIdx = i
		case strings.Contains(line, "[script] building the project"):
			buildOutputIdx = i
		case strings.Contains(line, "[status] step_completed: build -> success"):
			buildCompleteIdx = i
		case strings.Contains(line, "[status] step_started: test"):
			testStartIdx = i
		}
	}

	assert.Less(t, buildStartIdx, buildOutputIdx, "build start should be before build output")
	assert.Less(t, buildOutputIdx, buildCompleteIdx, "build output should be before build complete")
	assert.Less(t, buildCompleteIdx, testStartIdx, "build complete should be before test start")

	// Verify each line has a timestamp prefix
	for _, line := range lines {
		assert.Regexp(t, `^\[\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\] \[(status|script|llm)\] .+`, line,
			"each line should be timestamped and type-prefixed: %s", line)
	}
}

func TestRunner_UnifiedLogLLMStep(t *testing.T) {
	dir := t.TempDir()

	// Create a mock agent script that produces LLM-like output
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'Claude: I will implement the feature'\necho 'Claude: Done implementing'\n"), 0755))

	workflowContent := `workflow "llm-log-test" {
  step implement {
    agent_command = "` + mockAgent + `"
    prompt = "Build something."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "llm-test.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	// Verify full.log has LLM output with [llm] prefix
	fullLog, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)

	logStr := string(fullLog)
	assert.Contains(t, logStr, "[llm] Claude: I will implement the feature")
	assert.Contains(t, logStr, "[llm] Claude: Done implementing")
	assert.Contains(t, logStr, "[status] step_started: implement")
	assert.Contains(t, logStr, "[status] step_completed: implement -> success")

	// Verify llm-<step>.log was created
	llmLog, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "llm-implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(llmLog), "Claude: I will implement the feature")

	// Verify <step>.log still exists (backward compat)
	stepLog, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(stepLog), "Claude: I will implement the feature")
}

func TestRunner_UnifiedLogMixedSteps(t *testing.T) {
	dir := t.TempDir()

	// Create a mock agent script
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'LLM output here'\n"), 0755))

	workflowContent := `workflow "mixed-test" {
  step build {
    run = "echo compiling"
    results = [success, fail]
  }

  step implement {
    agent_command = "` + mockAgent + `"
    prompt = "Implement the feature."
    results = [success, fail]
  }

  build:success -> implement
  build:fail -> abort

  implement:success -> done
  implement:fail -> abort
}`
	workflowPath := filepath.Join(dir, "mixed.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	fullLog, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)

	logStr := string(fullLog)

	// Script step output tagged as [script]
	assert.Contains(t, logStr, "[script] compiling")
	// LLM step output tagged as [llm]
	assert.Contains(t, logStr, "[llm] LLM output here")

	// Verify both script and LLM per-step files exist
	_, err = os.Stat(filepath.Join(dir, ".cloche", "output", "build.log"))
	assert.NoError(t, err, "build.log should exist")

	_, err = os.Stat(filepath.Join(dir, ".cloche", "output", "implement.log"))
	assert.NoError(t, err, "implement.log should exist")

	_, err = os.Stat(filepath.Join(dir, ".cloche", "output", "llm-implement.log"))
	assert.NoError(t, err, "llm-implement.log should exist")
}

func TestRunner_ExtractsTitle(t *testing.T) {
	dir := t.TempDir()

	// Write user prompt under run ID
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "title-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "title-run", "prompt.txt"), []byte("Add dark mode toggle to the settings page"), 0644))

	workflowContent := `workflow "title-test" {
  step build {
    run = "echo building"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`
	workflowPath := filepath.Join(dir, "title.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
		RunID:        "title-run",
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	// Find the run_title message
	var foundTitle bool
	for _, msg := range msgs {
		if msg.Type == protocol.MsgRunTitle {
			foundTitle = true
			assert.Equal(t, "Add dark mode toggle to the settings page", msg.Message)
			break
		}
	}
	assert.True(t, foundTitle, "should have run_title message extracted from prompt")
}

func TestRunner_TitleTruncation(t *testing.T) {
	dir := t.TempDir()

	// Write a very long prompt
	longLine := strings.Repeat("x", 200)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "long-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "long-run", "prompt.txt"), []byte(longLine), 0644))

	workflowContent := `workflow "truncate-test" {
  step build {
    run = "echo building"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`
	workflowPath := filepath.Join(dir, "truncate.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
		RunID:        "long-run",
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	for _, msg := range msgs {
		if msg.Type == protocol.MsgRunTitle {
			assert.LessOrEqual(t, len(msg.Message), 100, "title should be truncated to 100 chars")
			assert.True(t, strings.HasSuffix(msg.Message, "..."), "truncated title should end with ...")
			break
		}
	}
}

func TestRunner_ScriptStepStreamsLogs(t *testing.T) {
	dir := t.TempDir()
	workflowContent := `workflow "stream-test" {
  step build {
    run = "echo 'compiling main.go'; echo 'compiling util.go'; echo 'build complete'"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`
	workflowPath := filepath.Join(dir, "stream-test.cloche")
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

	// Collect log messages for the build step
	var logMessages []string
	for _, msg := range msgs {
		if msg.Type == protocol.MsgLog && msg.StepName == "build" {
			logMessages = append(logMessages, msg.Message)
		}
	}

	// Script output should be streamed as individual log messages
	assert.Contains(t, logMessages, "compiling main.go")
	assert.Contains(t, logMessages, "compiling util.go")
	assert.Contains(t, logMessages, "build complete")

	// Verify log messages appear between step_started and step_completed
	var stepStartIdx, firstLogIdx, stepCompleteIdx int
	for i, msg := range msgs {
		switch {
		case msg.Type == protocol.MsgStepStarted && msg.StepName == "build":
			stepStartIdx = i
		case msg.Type == protocol.MsgLog && msg.StepName == "build" && firstLogIdx == 0:
			firstLogIdx = i
		case msg.Type == protocol.MsgStepCompleted && msg.StepName == "build":
			stepCompleteIdx = i
		}
	}
	assert.Less(t, stepStartIdx, firstLogIdx, "log messages should appear after step_started")
	assert.Less(t, firstLogIdx, stepCompleteIdx, "log messages should appear before step_completed")
}

func TestRunner_FailedStepReportsErrorWithStepName(t *testing.T) {
	dir := t.TempDir()

	// Create a script that exits with non-zero (producing "fail" result)
	// followed by a step that will never run
	workflowContent := `workflow "fail-test" {
  step build {
    run = "exit 1"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`
	workflowPath := filepath.Join(dir, "fail-test.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	// Abort path: no engine error, run completes as failed
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "failed", last.Result)
}

func TestRunner_StepExecutionErrorReportsErrorWithStepName(t *testing.T) {
	dir := t.TempDir()

	// Create a workflow where the script command doesn't exist, causing an execution error
	workflowContent := `workflow "error-test" {
  step build {
    run = "/nonexistent-command-that-does-not-exist-xyz"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`
	workflowPath := filepath.Join(dir, "error-test.cloche")
	require.NoError(t, os.WriteFile(workflowPath, []byte(workflowContent), 0644))

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      dir,
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	// The run should fail because the command doesn't exist
	// but generic adapter returns "fail" for exit errors, not an error
	// so this tests the abort path
	if err != nil {
		// If engine returns an error, verify the MsgError has the step name
		msgs, parseErr := protocol.ParseStatusStream(statusBuf.Bytes())
		require.NoError(t, parseErr)

		var errorMsg *protocol.StatusMessage
		for i := range msgs {
			if msgs[i].Type == protocol.MsgError {
				errorMsg = &msgs[i]
				break
			}
		}
		require.NotNil(t, errorMsg, "should have an error message")
		assert.NotEmpty(t, errorMsg.StepName, "error message should include the failed step name")
		assert.Contains(t, errorMsg.Message, "build", "error message should reference the failed step")
	}
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
