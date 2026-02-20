package integration_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/cloche-dev/cloche/internal/agent"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSmoke_AgentRunsWorkflowEndToEnd(t *testing.T) {
	dir := t.TempDir()
	workflowContent := `workflow "smoke-test" {
  step build {
    run = "echo building && echo 'built' > built.txt"
    results = [success, fail]
  }

  step verify {
    run = "test -f built.txt"
    results = [pass, fail]
  }

  build:success -> verify
  build:fail -> abort

  verify:pass -> done
  verify:fail -> abort
}`
	workflowPath := dir + "/smoke.cloche"
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

	// Verify the workflow executed both steps successfully
	var stepNames []string
	for _, msg := range msgs {
		if msg.Type == protocol.MsgStepStarted {
			stepNames = append(stepNames, msg.StepName)
		}
	}
	assert.Equal(t, []string{"build", "verify"}, stepNames)

	// Verify final status
	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)

	// Verify the build step actually created the file
	_, err = os.Stat(dir + "/built.txt")
	assert.NoError(t, err)
}
