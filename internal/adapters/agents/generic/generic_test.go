package generic_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericAdapter_ScriptSuccess(t *testing.T) {
	adapter := generic.New()
	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	sr, err := adapter.Execute(context.Background(), step, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
}

func TestGenericAdapter_ScriptFailure(t *testing.T) {
	adapter := generic.New()
	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "exit 1"},
	}

	sr, err := adapter.Execute(context.Background(), step, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

func TestGenericAdapter_ScriptModifiesFiles(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "generate",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'generated' > output.txt"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "generated")
}

func TestGenericAdapter_CapturesOutput(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "test",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'hello from test'; echo 'error msg' >&2"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	logPath := filepath.Join(dir, ".cloche", "output", "test.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from test")
	assert.Contains(t, string(content), "error msg")
}

func TestGenericAdapter_CapturesOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "lint",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'lint error: bad style'; exit 1"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)

	logPath := filepath.Join(dir, ".cloche", "output", "lint.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "lint error: bad style")
}

func TestGenericAdapter_StdoutMarkerOverridesExitCode(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"run": "echo 'analyzing...' && echo 'CLOCHE_RESULT:needs_research'"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", sr.Result)

	// Verify marker is stripped from log
	logPath := filepath.Join(dir, ".cloche", "output", "analyze.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "CLOCHE_RESULT")
	assert.Contains(t, string(content), "analyzing...")
}

func TestGenericAdapter_MarkerOverridesFailExitCode(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "triage",
		Type:    domain.StepTypeScript,
		Results: []string{"bug_fix", "feature_request"},
		Config:  map[string]string{"run": "echo 'CLOCHE_RESULT:bug_fix' && exit 1"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "bug_fix", sr.Result)
}

func TestGenericAdapter_PassesRunIDEnvVar(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	adapter.RunID = "test-run-42"

	step := &domain.Step{
		Name:    "check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $CLOCHE_RUN_ID"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	logPath := filepath.Join(dir, ".cloche", "output", "check.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "test-run-42")
}

func TestGenericAdapter_PassesProjectDirEnvVar(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	adapter.RunID = "test-run-42"

	step := &domain.Step{
		Name:    "check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $CLOCHE_PROJECT_DIR"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	logPath := filepath.Join(dir, ".cloche", "output", "check.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), dir)
}

func TestGenericAdapter_StreamsOutputViaStatusWriter(t *testing.T) {
	dir := t.TempDir()

	var statusBuf bytes.Buffer
	sw := protocol.NewStatusWriter(&statusBuf)

	adapter := generic.New()
	adapter.StatusWriter = sw

	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'line one'; echo 'line two'; echo 'line three'"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Parse status messages and verify log lines were streamed
	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	var logMessages []string
	for _, msg := range msgs {
		if msg.Type == protocol.MsgLog && msg.StepName == "build" {
			logMessages = append(logMessages, msg.Message)
		}
	}

	assert.Equal(t, []string{"line one", "line two", "line three"}, logMessages)

	// Verify the log file was still written
	logPath := filepath.Join(dir, ".cloche", "output", "build.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "line one")
	assert.Contains(t, string(content), "line two")
	assert.Contains(t, string(content), "line three")
}

func TestGenericAdapter_StreamsStderrViaStatusWriter(t *testing.T) {
	dir := t.TempDir()

	var statusBuf bytes.Buffer
	sw := protocol.NewStatusWriter(&statusBuf)

	adapter := generic.New()
	adapter.StatusWriter = sw

	step := &domain.Step{
		Name:    "lint",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'stdout msg'; echo 'stderr msg' >&2"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Parse status messages — both stdout and stderr should appear
	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	var logMessages []string
	for _, msg := range msgs {
		if msg.Type == protocol.MsgLog {
			logMessages = append(logMessages, msg.Message)
		}
	}
	assert.Contains(t, logMessages, "stdout msg")
	assert.Contains(t, logMessages, "stderr msg")
}

func TestGenericAdapter_StreamingWithMarker(t *testing.T) {
	dir := t.TempDir()

	var statusBuf bytes.Buffer
	sw := protocol.NewStatusWriter(&statusBuf)

	adapter := generic.New()
	adapter.StatusWriter = sw

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"run": "echo 'analyzing...' && echo 'CLOCHE_RESULT:needs_research'"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", sr.Result)

	// Verify marker is stripped from log file
	logPath := filepath.Join(dir, ".cloche", "output", "analyze.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "CLOCHE_RESULT")
	assert.Contains(t, string(content), "analyzing...")
}

func TestGenericAdapter_StreamingOnFailure(t *testing.T) {
	dir := t.TempDir()

	var statusBuf bytes.Buffer
	sw := protocol.NewStatusWriter(&statusBuf)

	adapter := generic.New()
	adapter.StatusWriter = sw

	step := &domain.Step{
		Name:    "test",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'running tests'; echo 'FAIL: something broke'; exit 1"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)

	// Verify output was streamed even on failure
	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	var logMessages []string
	for _, msg := range msgs {
		if msg.Type == protocol.MsgLog {
			logMessages = append(logMessages, msg.Message)
		}
	}
	assert.Contains(t, logMessages, "running tests")
	assert.Contains(t, logMessages, "FAIL: something broke")
}
