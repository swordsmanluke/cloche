//go:build regression

package regression

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const simpleHostWorkflow = `workflow "simple" {
  host {}

  step greet {
    run     = "echo hello-from-cloche"
    results = [success, fail]
  }

  greet:success -> done
  greet:fail    -> abort
}
`

const multiStepHostWorkflow = `workflow "multi" {
  host {}

  step first {
    run     = "echo step-one-output"
    results = [success, fail]
  }

  step second {
    run     = "echo step-two-output"
    results = [success, fail]
  }

  first:success  -> second
  first:fail     -> abort
  second:success -> done
  second:fail    -> abort
}
`

// TestRegression_HostWorkflow_GRPCLogStream validates the full pipeline:
// RunWorkflow -> host executor runs steps -> logs served via gRPC StreamLogs.
func TestRegression_HostWorkflow_GRPCLogStream(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "simple.cloche", simpleHostWorkflow)

	runID := env.RunWorkflow(t, "simple")
	env.WaitForState(t, runID, "succeeded", 30*time.Second)

	entries := env.StreamLogs(t, runID)
	require.NotEmpty(t, entries, "expected at least one log entry")

	// Verify we got a run_completed entry.
	last := entries[len(entries)-1]
	assert.Equal(t, "run_completed", last.Type)
	assert.Equal(t, "succeeded", last.Result)

	// Verify we received log content with "hello-from-cloche" somewhere in the stream.
	var foundGreet bool
	for _, e := range entries {
		if strings.Contains(e.Message, "hello-from-cloche") {
			foundGreet = true
			break
		}
	}
	assert.True(t, foundGreet, "expected to find 'hello-from-cloche' in streamed logs")
}

// TestRegression_HostWorkflow_SSEStream validates the HTTP SSE endpoint
// serves log lines for a completed host workflow run.
func TestRegression_HostWorkflow_SSEStream(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "simple.cloche", simpleHostWorkflow)

	runID := env.RunWorkflow(t, "simple")
	env.WaitForState(t, runID, "succeeded", 30*time.Second)

	events := env.CollectSSEEvents(t, runID, 10*time.Second)
	require.NotEmpty(t, events, "expected SSE events")

	// Should end with a "done" event.
	lastEvent := events[len(events)-1]
	assert.Equal(t, "done", lastEvent.Event)

	// Parse data lines and verify content.
	lines := SSEDataLines(events)
	var foundGreet bool
	for _, line := range lines {
		if strings.Contains(line.Content, "hello-from-cloche") {
			foundGreet = true
			break
		}
	}
	assert.True(t, foundGreet, "expected 'hello-from-cloche' in SSE data lines")
}

// TestRegression_HostWorkflow_MultiStep_GRPCLogStream verifies that a multi-step
// host workflow produces step_started/step_completed entries in the gRPC log stream.
func TestRegression_HostWorkflow_MultiStep_GRPCLogStream(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "multi.cloche", multiStepHostWorkflow)

	runID := env.RunWorkflow(t, "multi")
	env.WaitForState(t, runID, "succeeded", 30*time.Second)

	entries := env.StreamLogs(t, runID)
	require.NotEmpty(t, entries, "expected log entries")

	// Check for step events. For completed runs, StreamLogs serves from
	// full.log (type "full_log") or from captures (type "step_started"/"step_completed").
	// Either way, we should find evidence of both steps.
	var allContent string
	var stepsCompleted []string
	for _, e := range entries {
		allContent += e.Message + "\n"
		if e.Type == "step_completed" {
			stepsCompleted = append(stepsCompleted, e.StepName)
		}
	}

	// Verify both steps' output appears in the log content.
	assert.True(t, strings.Contains(allContent, "step-one-output") ||
		strings.Contains(allContent, "step_started: first"),
		"expected evidence of 'first' step in log stream")
	assert.True(t, strings.Contains(allContent, "step-two-output") ||
		strings.Contains(allContent, "step_started: second"),
		"expected evidence of 'second' step in log stream")

	// Verify final entry is run_completed.
	last := entries[len(entries)-1]
	assert.Equal(t, "run_completed", last.Type)
	assert.Equal(t, "succeeded", last.Result)
}

// TestRegression_CompletedRun_SSEServesFromDisk verifies that the SSE
// endpoint correctly serves archived full.log content for a completed run.
func TestRegression_CompletedRun_SSEServesFromDisk(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "multi.cloche", multiStepHostWorkflow)

	runID := env.RunWorkflow(t, "multi")
	env.WaitForState(t, runID, "succeeded", 30*time.Second)

	events := env.CollectSSEEvents(t, runID, 10*time.Second)
	require.NotEmpty(t, events)

	lastEvent := events[len(events)-1]
	assert.Equal(t, "done", lastEvent.Event)

	lines := SSEDataLines(events)
	var foundFirst, foundSecond bool
	for _, line := range lines {
		if strings.Contains(line.Content, "step-one-output") {
			foundFirst = true
		}
		if strings.Contains(line.Content, "step-two-output") {
			foundSecond = true
		}
	}
	assert.True(t, foundFirst, "expected 'step-one-output' in archived SSE data")
	assert.True(t, foundSecond, "expected 'step-two-output' in archived SSE data")
}

// TestRegression_HostWorkflow_LiveFollow validates the gRPC StreamLogs
// follow mode with a workflow that takes enough time for the subscription
// to catch live events.
func TestRegression_HostWorkflow_LiveFollow(t *testing.T) {
	env := NewTestEnv(t)

	slowWorkflow := `workflow "slow" {
  host {}

  step wait {
    run     = "sleep 1 && echo live-follow-output"
    results = [success, fail]
  }

  wait:success -> done
  wait:fail    -> abort
}
`
	env.WriteHostWorkflow(t, "slow.cloche", slowWorkflow)
	runID := env.RunWorkflow(t, "slow")

	// Give the run a moment to start and register in the broadcaster.
	time.Sleep(100 * time.Millisecond)

	entries := env.StreamLogsFollow(t, runID)

	var collected []string
	timeout := time.After(30 * time.Second)
	for {
		select {
		case entry, ok := <-entries:
			if !ok {
				goto done
			}
			collected = append(collected, entry.Type+":"+entry.Message)
			if entry.Type == "run_completed" {
				goto done
			}
		case <-timeout:
			t.Fatal("timed out waiting for run_completed in follow mode")
		}
	}
done:

	require.NotEmpty(t, collected, "expected log entries from follow mode")

	// Verify we received some live content.
	var foundOutput bool
	for _, s := range collected {
		if strings.Contains(s, "live-follow-output") {
			foundOutput = true
		}
	}
	assert.True(t, foundOutput, "expected 'live-follow-output' from live follow stream")
}
