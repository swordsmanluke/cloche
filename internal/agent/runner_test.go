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

func TestRunner_ExecutesWorkflowFile(t *testing.T) {
	dir := t.TempDir()
	workflowContent := `workflow "simple-build" {
  step build {
    run = "echo building"
    results = [success, fail]
  }

  step test {
    run = "echo testing"
    results = [pass, fail]
  }

  build:success -> test
  build:fail -> abort

  test:pass -> done
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
