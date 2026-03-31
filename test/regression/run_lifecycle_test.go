//go:build regression

package regression

import (
	"context"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// completedSteps filters StepExecutions to only those with a Result (completed entries).
// The captures store writes separate rows for step_started and step_completed.
func completedSteps(execs []*pb.StepExecutionStatus) map[string]string {
	results := make(map[string]string)
	for _, se := range execs {
		if se.Result != "" {
			results[se.StepName] = se.Result
		}
	}
	return results
}

// TestRegression_HostWorkflow_StatusTransitions verifies that a host workflow
// run transitions through states correctly and appears in ListRuns.
func TestRegression_HostWorkflow_StatusTransitions(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "simple.cloche", simpleHostWorkflow)

	runID := env.RunWorkflow(t, "simple")

	// Wait for completion.
	status := env.WaitForState(t, runID, "succeeded", 30*time.Second)
	assert.Equal(t, "simple", status.WorkflowName)
	assert.True(t, status.IsHost, "expected host workflow flag")

	// Verify step executions are recorded.
	steps := completedSteps(status.StepExecutions)
	require.NotEmpty(t, steps, "expected at least one completed step execution")
	assert.Equal(t, "success", steps["greet"])

	// Verify run appears in ListRuns.
	listResp, err := env.GRPCClient.ListRuns(context.Background(), &pb.ListRunsRequest{
		ProjectDir: env.ProjectDir,
	})
	require.NoError(t, err)

	var found bool
	for _, run := range listResp.Runs {
		if run.RunId == runID {
			found = true
			assert.Equal(t, "succeeded", run.State)
			break
		}
	}
	assert.True(t, found, "run %s should appear in ListRuns", runID)
}

// TestRegression_HostWorkflow_StepFailure verifies that a failing step
// causes the run to end in failed state.
func TestRegression_HostWorkflow_StepFailure(t *testing.T) {
	env := NewTestEnv(t)

	failWorkflow := `workflow "failing" {
  host {}

  step bad-step {
    run     = "exit 1"
    results = [success, fail]
  }

  bad-step:success -> done
  bad-step:fail    -> abort
}
`
	env.WriteHostWorkflow(t, "failing.cloche", failWorkflow)

	runID := env.RunWorkflow(t, "failing")
	status := env.WaitForState(t, runID, "failed", 30*time.Second)

	assert.Equal(t, "failing", status.WorkflowName)

	// Verify the step recorded a failure.
	steps := completedSteps(status.StepExecutions)
	require.NotEmpty(t, steps)
	assert.Equal(t, "fail", steps["bad-step"])
}

// TestRegression_HostWorkflow_MultiStepLifecycle verifies that a multi-step
// host workflow executes steps in order and records all step executions.
func TestRegression_HostWorkflow_MultiStepLifecycle(t *testing.T) {
	env := NewTestEnv(t)
	env.WriteHostWorkflow(t, "multi.cloche", multiStepHostWorkflow)

	runID := env.RunWorkflow(t, "multi")
	status := env.WaitForState(t, runID, "succeeded", 30*time.Second)

	// Both steps should have completed successfully.
	steps := completedSteps(status.StepExecutions)
	require.Len(t, steps, 2, "expected 2 completed step executions")
	assert.Equal(t, "success", steps["first"])
	assert.Equal(t, "success", steps["second"])
}
