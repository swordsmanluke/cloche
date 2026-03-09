package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDispatcher records dispatch calls and returns a predetermined run ID.
type fakeDispatcher struct {
	calls     []*pb.RunWorkflowRequest
	runID     string
	returnErr error
}

func (f *fakeDispatcher) RunWorkflow(_ context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	f.calls = append(f.calls, req)
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return &pb.RunWorkflowResponse{RunId: f.runID}, nil
}

// fakeStore returns a predetermined run on GetRun.
type fakeStore struct {
	runs map[string]*domain.Run
}

func (f *fakeStore) CreateRun(_ context.Context, run *domain.Run) error {
	f.runs[run.ID] = run
	return nil
}
func (f *fakeStore) GetRun(_ context.Context, id string) (*domain.Run, error) {
	if r, ok := f.runs[id]; ok {
		return r, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeStore) UpdateRun(_ context.Context, run *domain.Run) error {
	f.runs[run.ID] = run
	return nil
}
func (f *fakeStore) DeleteRun(_ context.Context, id string) error {
	delete(f.runs, id)
	return nil
}
func (f *fakeStore) ListRuns(_ context.Context, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeStore) ListRunsByProject(_ context.Context, _ string, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeStore) ListProjects(_ context.Context) ([]string, error) {
	return nil, nil
}

func TestExecutor_ScriptStep_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "greet",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Check output was written
	data, err := os.ReadFile(filepath.Join(outputDir, "greet.out"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

func TestExecutor_ScriptStep_Failure(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "bad",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "exit 1"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
}

func TestExecutor_ScriptStep_ResultMarker(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "marker",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail", "custom"},
		Config:  map[string]string{"run": "echo 'some output'; echo 'CLOCHE_RESULT:custom'"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "custom", result)

	// Marker line should be stripped from output
	data, err := os.ReadFile(filepath.Join(outputDir, "marker.out"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "CLOCHE_RESULT")
	assert.Contains(t, string(data), "some output")
}

func TestExecutor_WorkflowStep_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	store := &fakeStore{runs: map[string]*domain.Run{
		"develop-test-run": {
			ID:    "develop-test-run",
			State: domain.RunStateSucceeded,
		},
	}}

	dispatcher := &fakeDispatcher{runID: "develop-test-run"}

	executor := &Executor{
		ProjectDir: tmpDir,
		Dispatcher: dispatcher,
		Store:      store,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Len(t, dispatcher.calls, 1)
	assert.Equal(t, "develop", dispatcher.calls[0].WorkflowName)
	assert.Equal(t, tmpDir, dispatcher.calls[0].ProjectDir)
}

func TestExecutor_WorkflowStep_Failure(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	store := &fakeStore{runs: map[string]*domain.Run{
		"develop-test-run": {
			ID:    "develop-test-run",
			State: domain.RunStateFailed,
		},
	}}

	dispatcher := &fakeDispatcher{runID: "develop-test-run"}

	executor := &Executor{
		ProjectDir: tmpDir,
		Dispatcher: dispatcher,
		Store:      store,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
}

func TestExecutor_WorkflowStep_PassesPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	// Write a previous step's output
	_ = os.WriteFile(filepath.Join(outputDir, "prepare-prompt.out"), []byte("the task prompt"), 0644)

	store := &fakeStore{runs: map[string]*domain.Run{
		"develop-test-run": {
			ID:    "develop-test-run",
			State: domain.RunStateSucceeded,
		},
	}}

	dispatcher := &fakeDispatcher{runID: "develop-test-run"}

	executor := &Executor{
		ProjectDir: tmpDir,
		Dispatcher: dispatcher,
		Store:      store,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"workflow_name": "develop",
			"prompt_step":   "prepare-prompt",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, "the task prompt", dispatcher.calls[0].Prompt)
}

func TestEngine_HostWorkflow_Linear(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	store := &fakeStore{runs: map[string]*domain.Run{
		"develop-test-run": {
			ID:    "develop-test-run",
			State: domain.RunStateSucceeded,
		},
	}}

	dispatcher := &fakeDispatcher{runID: "develop-test-run"}

	executor := &Executor{
		ProjectDir: tmpDir,
		Dispatcher: dispatcher,
		Store:      store,
		OutputDir:  outputDir,
	}

	wf := &domain.Workflow{
		Name: "main",
		Steps: map[string]*domain.Step{
			"prepare": {
				Name:    "prepare",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo 'prepared'"},
			},
			"develop": {
				Name:    "develop",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"workflow_name": "develop"},
			},
			"merge": {
				Name:    "merge",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo 'merged'"},
			},
		},
		Wiring: []domain.Wire{
			{From: "prepare", Result: "success", To: "develop"},
			{From: "prepare", Result: "fail", To: domain.StepAbort},
			{From: "develop", Result: "success", To: "merge"},
			{From: "develop", Result: "fail", To: domain.StepAbort},
			{From: "merge", Result: "success", To: domain.StepDone},
			{From: "merge", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "prepare",
	}

	eng := engine.New(executor)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Len(t, dispatcher.calls, 1)
}

func TestEngine_HostWorkflow_AbortOnScriptFail(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	wf := &domain.Workflow{
		Name: "main",
		Steps: map[string]*domain.Step{
			"prepare": {
				Name:    "prepare",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "exit 1"},
			},
			"develop": {
				Name:    "develop",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"workflow_name": "develop"},
			},
		},
		Wiring: []domain.Wire{
			{From: "prepare", Result: "success", To: "develop"},
			{From: "prepare", Result: "fail", To: domain.StepAbort},
			{From: "develop", Result: "success", To: domain.StepDone},
			{From: "develop", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "prepare",
	}

	eng := engine.New(executor)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
}

func TestExecutor_ScriptStep_EnvironmentVars(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "env-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $CLOCHE_PROJECT_DIR"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "env-check.out"))
	require.NoError(t, err)
	assert.Contains(t, string(data), tmpDir)
}
