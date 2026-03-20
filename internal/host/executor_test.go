package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/runcontext"
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
func (f *fakeStore) GetRunByAttempt(_ context.Context, attemptID, id string) (*domain.Run, error) {
	if r, ok := f.runs[id]; ok && r.AttemptID == attemptID {
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
func (f *fakeStore) ListRunsFiltered(_ context.Context, _ domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeStore) ListProjects(_ context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) ListChildRuns(_ context.Context, parentRunID string) ([]*domain.Run, error) {
	var children []*domain.Run
	for _, r := range f.runs {
		if r.ParentRunID == parentRunID {
			children = append(children, r)
		}
	}
	return children, nil
}
func (f *fakeStore) QueryUsage(_ context.Context, _ ports.UsageQuery) ([]domain.UsageSummary, error) {
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
	data, err := os.ReadFile(filepath.Join(outputDir, "greet.log"))
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
	data, err := os.ReadFile(filepath.Join(outputDir, "marker.log"))
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

func TestExecutor_WorkflowStep_StoresChildRunID(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	store := &fakeStore{runs: map[string]*domain.Run{
		"develop-child-run": {
			ID:    "develop-child-run",
			State: domain.RunStateSucceeded,
		},
	}}

	dispatcher := &fakeDispatcher{runID: "develop-child-run"}
	hostRunID := "main-host-run"
	taskID := "main-task-id"

	executor := &Executor{
		ProjectDir: tmpDir,
		Dispatcher: dispatcher,
		Store:      store,
		OutputDir:  outputDir,
		HostRunID:  hostRunID,
		TaskID:     taskID,
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

	// Verify child_run_id was stored in run context
	val, ok, err := runcontext.Get(tmpDir, taskID, "child_run_id")
	require.NoError(t, err)
	assert.True(t, ok, "child_run_id should be stored in run context")
	assert.Equal(t, "develop-child-run", val)
}

func TestExecutor_WorkflowStep_PassesPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(outputDir, 0755)

	// Write a previous step's output
	_ = os.WriteFile(filepath.Join(outputDir, "prepare-prompt.log"), []byte("the task prompt"), 0644)

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

	data, err := os.ReadFile(filepath.Join(outputDir, "env-check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), tmpDir)
}

func TestExecutor_ScriptStep_RunIDEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "develop-swift-oak-a1b2",
	}

	step := &domain.Step{
		Name:    "runid-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $CLOCHE_RUN_ID"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "runid-check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "develop-swift-oak-a1b2")
}

func TestExecutor_ScriptStep_NoRunID(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		// No HostRunID set
	}

	step := &domain.Step{
		Name:    "runid-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo \"RUN_ID=$CLOCHE_RUN_ID\""},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "runid-check.log"))
	require.NoError(t, err)
	// When no HostRunID is set, CLOCHE_RUN_ID should not appear in the env
	assert.Contains(t, string(data), "RUN_ID=\n")
}

func TestExecutor_ScriptStep_TaskIDEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		TaskID:     "my-task-42",
	}

	step := &domain.Step{
		Name:    "task-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $CLOCHE_TASK_ID"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "task-check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-task-42")
}

func TestExecutor_ScriptStep_NoTaskID(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		// No TaskID set
	}

	step := &domain.Step{
		Name:    "task-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo \"TASK_ID=$CLOCHE_TASK_ID\""},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "task-check.log"))
	require.NoError(t, err)
	// When no TaskID is set, CLOCHE_TASK_ID should not appear in the env
	assert.Contains(t, string(data), "TASK_ID=\n")
}

func TestExecutor_ScriptStep_NoOutputMappings(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		Wires: []domain.Wire{
			{From: "step-a", Result: "success", To: "step-b"},
		},
	}

	step := &domain.Step{
		Name:    "step-b",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
}

func TestExecutor_ScriptStep_OutputMappings_JSONFields(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Write source step output as JSON
	sourceJSON := `{"title": "Fix bug", "priority": "high"}`
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "get-tasks.log"), []byte(sourceJSON), 0644))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		Wires: []domain.Wire{
			{
				From:   "get-tasks",
				Result: "success",
				To:     "use-tasks",
				OutputMap: []domain.OutputMapping{
					{
						EnvVar: "TASK_TITLE",
						Path:   domain.OutputPath{Segments: []domain.PathSegment{{Kind: domain.SegmentField, Field: "title"}}},
					},
					{
						EnvVar: "TASK_PRIORITY",
						Path:   domain.OutputPath{Segments: []domain.PathSegment{{Kind: domain.SegmentField, Field: "priority"}}},
					},
				},
			},
		},
	}

	step := &domain.Step{
		Name:    "use-tasks",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $TASK_TITLE $TASK_PRIORITY"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "use-tasks.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Fix bug")
	assert.Contains(t, string(data), "high")
}

func TestExecutor_ScriptStep_OutputMappings_ArrayIndex(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Write source step output as JSON array
	sourceJSON := `[{"id": "task-1", "title": "First"}, {"id": "task-2", "title": "Second"}]`
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "list-tasks.log"), []byte(sourceJSON), 0644))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		Wires: []domain.Wire{
			{
				From:   "list-tasks",
				Result: "success",
				To:     "process",
				OutputMap: []domain.OutputMapping{
					{
						EnvVar: "FIRST_ID",
						Path: domain.OutputPath{Segments: []domain.PathSegment{
							{Kind: domain.SegmentIndex, Index: 0},
							{Kind: domain.SegmentField, Field: "id"},
						}},
					},
					{
						EnvVar: "SECOND_TITLE",
						Path: domain.OutputPath{Segments: []domain.PathSegment{
							{Kind: domain.SegmentIndex, Index: 1},
							{Kind: domain.SegmentField, Field: "title"},
						}},
					},
				},
			},
		},
	}

	step := &domain.Step{
		Name:    "process",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo $FIRST_ID $SECOND_TITLE"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "process.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "task-1")
	assert.Contains(t, string(data), "Second")
}

func TestExecutor_ScriptStep_OutputMappings_MissingSourceOutput(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// No source output file written

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		Wires: []domain.Wire{
			{
				From:   "missing-step",
				Result: "success",
				To:     "consumer",
				OutputMap: []domain.OutputMapping{
					{
						EnvVar: "VAL",
						Path:   domain.OutputPath{Segments: []domain.PathSegment{{Kind: domain.SegmentField, Field: "key"}}},
					},
				},
			},
		},
	}

	step := &domain.Step{
		Name:    "consumer",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	_, err := executor.Execute(context.Background(), step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading output of missing-step")
}

func TestExecutor_ScriptStep_OutputMappings_NotJSON(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Write non-JSON output
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "plain.log"), []byte("not json"), 0644))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		Wires: []domain.Wire{
			{
				From:   "plain",
				Result: "success",
				To:     "consumer",
				OutputMap: []domain.OutputMapping{
					{
						EnvVar: "VAL",
						Path:   domain.OutputPath{Segments: []domain.PathSegment{{Kind: domain.SegmentField, Field: "key"}}},
					},
				},
			},
		},
	}

	step := &domain.Step{
		Name:    "consumer",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	_, err := executor.Execute(context.Background(), step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestRunner_RunWithID(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a simple host.cloche
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step greet {
    run     = "echo hi"
    results = [success, fail]
  }

  greet:success -> done
  greet:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}

	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	customID := "my-custom-run-id"
	result, err := runner.RunWithID(context.Background(), tmpDir, customID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)
	assert.Equal(t, customID, result.RunID, "RunWithID should use the provided ID")

	// Verify the run was persisted with the custom ID
	hostRun, err := store.GetRun(context.Background(), customID)
	require.NoError(t, err)
	assert.True(t, hostRun.IsHost)
	assert.Equal(t, domain.RunStateSucceeded, hostRun.State)
}

func TestRunner_WithTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	// The script echoes the daemon-assigned CLOCHE_TASK_ID env var
	hostCloche := `workflow "main" {
  host {}

  step check-task {
    run     = "echo $CLOCHE_TASK_ID"
    results = [success, fail]
  }

  check-task:success -> done
  check-task:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}

	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
		TaskID:     "daemon-assigned-task-99",
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Verify the script saw the task ID.
	// Use result.OutputDir directly — it is set by the runner to the correct path.
	data, err := os.ReadFile(filepath.Join(result.OutputDir, "check-task.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "daemon-assigned-task-99")
}

func TestRunner_PersistsHostRun(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a simple host.cloche
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step prepare {
    run     = "echo prepared"
    results = [success, fail]
  }

  prepare:success -> done
  prepare:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}

	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)
	assert.NotEmpty(t, result.RunID)

	// Verify the host run was persisted in the store
	hostRun, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	assert.True(t, hostRun.IsHost, "host run should have IsHost=true")
	assert.Equal(t, tmpDir, hostRun.ProjectDir)
	assert.Equal(t, domain.RunStateSucceeded, hostRun.State)
	assert.False(t, hostRun.StartedAt.IsZero())
	assert.False(t, hostRun.CompletedAt.IsZero())
}

func TestExecutor_ScriptStep_UsesMainDir(t *testing.T) {
	// ProjectDir has no script; MainDir has the script.
	projectDir := t.TempDir()
	mainDir := t.TempDir()
	outputDir := filepath.Join(projectDir, "output")

	// Create a script only in mainDir
	scriptDir := filepath.Join(mainDir, ".cloche", "scripts")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "greet.sh"),
		[]byte("#!/bin/sh\necho from-main"),
		0755,
	))

	executor := &Executor{
		ProjectDir: projectDir,
		MainDir:    mainDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "greet",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "bash .cloche/scripts/greet.sh"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "greet.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "from-main")
}

func TestExecutor_ScriptStep_MainDirOverridesProjectDir(t *testing.T) {
	// Both dirs have a script with different content; MainDir's version wins.
	projectDir := t.TempDir()
	mainDir := t.TempDir()
	outputDir := filepath.Join(projectDir, "output")

	// Create script in both dirs with different content
	for _, dir := range []string{projectDir, mainDir} {
		scriptDir := filepath.Join(dir, ".cloche", "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0755))
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, ".cloche", "scripts", "hello.sh"),
		[]byte("#!/bin/sh\necho from-branch"),
		0755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(mainDir, ".cloche", "scripts", "hello.sh"),
		[]byte("#!/bin/sh\necho from-main"),
		0755,
	))

	executor := &Executor{
		ProjectDir: projectDir,
		MainDir:    mainDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "hello",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "bash .cloche/scripts/hello.sh"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "hello.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "from-main")
	assert.NotContains(t, string(data), "from-branch")
}

func TestExecutor_ScriptStep_FallsBackToProjectDir(t *testing.T) {
	// When MainDir is empty, scripts resolve from ProjectDir.
	projectDir := t.TempDir()
	outputDir := filepath.Join(projectDir, "output")

	executor := &Executor{
		ProjectDir: projectDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "pwd-check",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "pwd"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "pwd-check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), projectDir)
}

func TestExecutor_ScriptStep_ProjectDirEnvVar(t *testing.T) {
	// CLOCHE_PROJECT_DIR should always point to ProjectDir, not MainDir.
	projectDir := t.TempDir()
	mainDir := t.TempDir()
	outputDir := filepath.Join(projectDir, "output")

	executor := &Executor{
		ProjectDir: projectDir,
		MainDir:    mainDir,
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

	data, err := os.ReadFile(filepath.Join(outputDir, "env-check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), projectDir)
	assert.NotContains(t, string(data), mainDir)
}

func TestMainWorktreeDir_NonGitDir(t *testing.T) {
	tmpDir := t.TempDir()
	// Non-git directory should fall back to projectDir.
	result := MainWorktreeDir(tmpDir)
	assert.Equal(t, tmpDir, result)
}

func TestMainWorktreeDir_WithWorktree(t *testing.T) {
	// Set up a real git repo with a linked worktree.
	mainDir := t.TempDir()

	// Initialize git repo
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", args, out)
	}

	run(mainDir, "git", "init")
	run(mainDir, "git", "config", "user.email", "test@test.com")
	run(mainDir, "git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(mainDir, "file.txt"), []byte("hello"), 0644))
	run(mainDir, "git", "add", ".")
	run(mainDir, "git", "commit", "-m", "initial")

	// Create a branch and worktree
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	run(mainDir, "git", "worktree", "add", worktreeDir, "-b", "feature")

	// MainWorktreeDir from the linked worktree should return mainDir.
	result := MainWorktreeDir(worktreeDir)
	assert.Equal(t, mainDir, result)

	// MainWorktreeDir from main should return mainDir.
	result = MainWorktreeDir(mainDir)
	assert.Equal(t, mainDir, result)
}

func TestExecutor_AgentStep_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create a mock agent script that reads stdin and produces output
	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'agent did the work'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
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
	assert.Equal(t, "success", result)

	// Check output was copied to executor's output path
	data, err := os.ReadFile(filepath.Join(outputDir, "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent did the work")
}

func TestExecutor_AgentStep_Failure(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create a mock agent that reports failure via result marker
	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'something went wrong'\necho 'CLOCHE_RESULT:fail'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
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
	assert.Equal(t, "fail", result)
}

func TestExecutor_AgentStep_WorkflowLevelCommand(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create a mock agent
	mockAgent := filepath.Join(tmpDir, "workflow-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'workflow agent ran'\n"), 0755))

	executor := &Executor{
		ProjectDir:    tmpDir,
		OutputDir:     outputDir,
		HostRunID:     "test-host-run",
		AgentCommands: []string{mockAgent},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt": "Write some code.",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "workflow agent ran")
}

func TestExecutor_AgentStep_StepLevelOverridesWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create two mock agents
	workflowAgent := filepath.Join(tmpDir, "workflow-agent.sh")
	require.NoError(t, os.WriteFile(workflowAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'workflow agent'\n"), 0755))

	stepAgent := filepath.Join(tmpDir, "step-agent.sh")
	require.NoError(t, os.WriteFile(stepAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'step agent'\n"), 0755))

	executor := &Executor{
		ProjectDir:    tmpDir,
		OutputDir:     outputDir,
		HostRunID:     "test-host-run",
		AgentCommands: []string{workflowAgent},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Write some code.",
			"agent_command": stepAgent,
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Step-level agent should have run, not workflow-level
	data, err := os.ReadFile(filepath.Join(outputDir, "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "step agent")
	assert.NotContains(t, string(data), "workflow agent")
}

func TestExecutor_AgentStep_FallbackChain(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create a good agent
	goodAgent := filepath.Join(tmpDir, "good-agent.sh")
	require.NoError(t, os.WriteFile(goodAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'fallback agent ran'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
	}

	// Use nonexistent command as primary, good agent as fallback
	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Write some code.",
			"agent_command": "nonexistent-agent-xyz," + goodAgent,
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "fallback agent ran")
}

func TestExecutor_AgentStep_PrevOutput(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Write previous step output
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "prepare.log"), []byte("the task description"), 0644))

	// Create a mock agent that echoes stdin (the prompt) to verify it received it
	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'processed prompt'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
		TaskID:     "test-task-id",
		Wires: []domain.Wire{
			{From: "prepare", Result: "success", To: "implement"},
		},
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
	assert.Equal(t, "success", result)

	// Verify prompt.txt was written with previous step's output
	promptPath := filepath.Join(tmpDir, ".cloche", "runs", "test-task-id", "prompt.txt")
	data, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, "the task description", string(data))
}

func TestExecutor_AgentStep_PromptStep(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Write a specific step's output
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "custom-source.log"), []byte("custom prompt content"), 0644))

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'done'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
		TaskID:     "test-task-id",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
			"prompt_step":   "custom-source",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify prompt.txt was written with the prompt_step output
	promptPath := filepath.Join(tmpDir, ".cloche", "runs", "test-task-id", "prompt.txt")
	data, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, "custom prompt content", string(data))
}

func TestEngine_HostWorkflow_WithAgentStep(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Create a mock agent
	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'agent output'\n"), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "test-host-run",
		TaskID:     "test-task-id",
	}

	wf := &domain.Workflow{
		Name: "main",
		Steps: map[string]*domain.Step{
			"prepare": {
				Name:    "prepare",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo 'task prompt'"},
			},
			"implement": {
				Name:    "implement",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
				Config: map[string]string{
					"prompt":        "You are a coding assistant.",
					"agent_command": mockAgent,
				},
			},
			"verify": {
				Name:    "verify",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo verified"},
			},
		},
		Wiring: []domain.Wire{
			{From: "prepare", Result: "success", To: "implement"},
			{From: "prepare", Result: "fail", To: domain.StepAbort},
			{From: "implement", Result: "success", To: "verify"},
			{From: "implement", Result: "fail", To: domain.StepAbort},
			{From: "verify", Result: "success", To: domain.StepDone},
			{From: "verify", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "prepare",
	}

	eng := engine.New(executor)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
}

func TestRunner_HostWorkflow_AgentStep(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock agent
	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'agent implemented'\n"), 0755))

	// Write a host.cloche with an agent step
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {
    agent_command = "` + mockAgent + `"
  }

  step implement {
    prompt = "Implement the feature."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}

	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Verify the run completed
	hostRun, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	assert.True(t, hostRun.IsHost)
	assert.Equal(t, domain.RunStateSucceeded, hostRun.State)
}

func TestRunner_HostWorkflow_AgentStepOverridesWorkflowCommand(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two mock agents
	workflowAgent := filepath.Join(tmpDir, "workflow-agent.sh")
	require.NoError(t, os.WriteFile(workflowAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'workflow level'\n"), 0755))

	stepAgent := filepath.Join(tmpDir, "step-agent.sh")
	require.NoError(t, os.WriteFile(stepAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'step level'\n"), 0755))

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {
    agent_command = "` + workflowAgent + `"
  }

  step implement {
    agent_command = "` + stepAgent + `"
    prompt = "Implement the feature."
    results = [success, fail]
  }

  implement:success -> done
  implement:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)
}

func TestRunner_PersistsHostRunOnFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a host.cloche that will fail
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step bad {
    run     = "exit 1"
    results = [success, fail]
  }

  bad:success -> done
  bad:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}

	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, result.State)

	// Verify the host run was persisted with failed state
	hostRun, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	assert.True(t, hostRun.IsHost)
	assert.Equal(t, domain.RunStateFailed, hostRun.State)
	assert.False(t, hostRun.CompletedAt.IsZero())
}

// --- RunNamed tests ---

func TestRunner_RunNamed_Main(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step greet {
    run     = "echo hi"
    results = [success, fail]
  }
  greet:success -> done
  greet:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.RunNamed(context.Background(), tmpDir, "main")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)
	assert.NotEmpty(t, result.OutputDir)
}

func TestRunner_RunNamed_MultiWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// host.cloche with three workflows
	hostCloche := `workflow "list-tasks" {
  host {}

  step fetch {
    run     = "echo '{\"id\":\"t1\",\"status\":\"open\",\"title\":\"Fix bug\"}'"
    results = [success, fail]
  }
  fetch:success -> done
  fetch:fail    -> abort
}

workflow "main" {
  host {}

  step work {
    run     = "echo working"
    results = [success, fail]
  }
  work:success -> done
  work:fail    -> abort
}

workflow "finalize" {
  host {}

  step cleanup {
    run     = "echo cleaned up"
    results = [success, fail]
  }
  cleanup:success -> done
  cleanup:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	// Run list-tasks workflow
	result, err := runner.RunNamed(context.Background(), tmpDir, "list-tasks")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Verify output was captured
	data, err := os.ReadFile(filepath.Join(result.OutputDir, "fetch.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "t1")

	// Run main workflow
	result, err = runner.RunNamed(context.Background(), tmpDir, "main")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Run finalize workflow
	result, err = runner.RunNamed(context.Background(), tmpDir, "finalize")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)
}

func TestRunner_RunNamed_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step greet {
    run     = "echo hi"
    results = [success, fail]
  }
  greet:success -> done
  greet:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	_, err := runner.RunNamed(context.Background(), tmpDir, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- ExtraEnv tests ---

func TestExecutor_ScriptStep_ExtraEnv(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		ExtraEnv: []string{
			"CLOCHE_MAIN_OUTCOME=succeeded",
			"CLOCHE_MAIN_RUN_ID=main-run-123",
		},
	}

	step := &domain.Step{
		Name:    "check-env",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo \"OUTCOME=$CLOCHE_MAIN_OUTCOME RUN=$CLOCHE_MAIN_RUN_ID\""},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	data, err := os.ReadFile(filepath.Join(outputDir, "check-env.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "OUTCOME=succeeded")
	assert.Contains(t, string(data), "RUN=main-run-123")
}

func TestRunner_ExtraEnv_Propagated(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "finalize" {
  host {}

  step check {
    run     = "echo OUTCOME=$CLOCHE_MAIN_OUTCOME"
    results = [success, fail]
  }
  check:success -> done
  check:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
		ExtraEnv:   []string{"CLOCHE_MAIN_OUTCOME=succeeded"},
	}

	result, err := runner.RunNamed(context.Background(), tmpDir, "finalize")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Verify the env var was available to the script
	data, err := os.ReadFile(filepath.Join(result.OutputDir, "check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "OUTCOME=succeeded")
}

func TestRunner_ExtraEnv_RestoredOnResume(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "finalize" {
  host {}

  step route {
    run     = "echo routed"
    results = [success, fail]
  }
  step merge {
    run     = "echo RUN=$CLOCHE_MAIN_RUN_ID"
    results = [success, fail]
  }
  route:success -> merge
  route:fail    -> abort
  merge:success -> done
  merge:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
		ExtraEnv:   []string{"CLOCHE_MAIN_RUN_ID=develop-original-branch"},
	}

	// Run the workflow — it succeeds and persists ExtraEnv to context.json
	result, err := runner.RunNamed(context.Background(), tmpDir, "finalize")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Simulate a failed run for resume: change state and record step results
	run, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	run.State = domain.RunStateFailed
	run.StepExecutions = []*domain.StepExecution{
		{StepName: "route", Result: "success"},
		{StepName: "merge", Result: "fail"},
	}
	require.NoError(t, store.UpdateRun(context.Background(), run))

	// Resume from merge with NO ExtraEnv on the runner — it should restore from context
	resumeRunner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run-2"},
		Store:      store,
	}

	resumeResult, err := resumeRunner.ResumeRun(context.Background(), run, "merge")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, resumeResult.State)

	// Verify the merge step got CLOCHE_MAIN_RUN_ID from restored ExtraEnv
	data, err := os.ReadFile(filepath.Join(resumeResult.OutputDir, "merge.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "RUN=develop-original-branch")
}

// --- ReadListTasksOutput tests ---

func TestReadListTasksOutput_Basic(t *testing.T) {
	outputDir := t.TempDir()

	// Write JSONL task output
	jsonl := `{"id":"task-1","status":"open","title":"Fix bug"}
{"id":"task-2","status":"closed","title":"Old bug"}`
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "fetch.log"), []byte(jsonl), 0644))

	tasks, err := ReadListTasksOutput(outputDir)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Equal(t, "open", tasks[0].Status)
	assert.Equal(t, "task-2", tasks[1].ID)
}

func TestReadListTasksOutput_EmptyDir(t *testing.T) {
	outputDir := t.TempDir()
	_, err := ReadListTasksOutput(outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no output files")
}

func TestReadListTasksOutput_MultipleFiles_PicksLatest(t *testing.T) {
	outputDir := t.TempDir()

	// Write an older file
	oldPath := filepath.Join(outputDir, "old-step.log")
	require.NoError(t, os.WriteFile(oldPath, []byte(`{"id":"old","status":"open"}`), 0644))

	// Ensure different mod times by sleeping briefly
	time.Sleep(50 * time.Millisecond)

	// Write a newer file
	newPath := filepath.Join(outputDir, "new-step.log")
	require.NoError(t, os.WriteFile(newPath, []byte(`{"id":"new","status":"open"}`), 0644))

	tasks, err := ReadListTasksOutput(outputDir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "new", tasks[0].ID)
}

// --- RunResult.OutputDir tests ---

func TestRunResult_HasOutputDir(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "main" {
  host {}

  step greet {
    run     = "echo hi"
    results = [success, fail]
  }
  greet:success -> done
  greet:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	result, err := runner.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.NotEmpty(t, result.OutputDir)

	// Verify output dir exists and contains step output
	_, err = os.Stat(result.OutputDir)
	assert.NoError(t, err)
}

// --- RunListTasksWorkflow tests ---

func TestRunListTasksWorkflow_EmptyResult_NoRunRecord(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// list-tasks workflow that outputs no tasks (empty line)
	hostCloche := `workflow "list-tasks" {
  host {}

  step fetch {
    run     = "echo ''"
    results = [success, fail]
  }
  fetch:success -> done
  fetch:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	tasks, result, err := RunListTasksWorkflow(context.Background(), runner, tmpDir)
	require.NoError(t, err)
	assert.Empty(t, tasks)
	assert.NotNil(t, result)

	// No run record should have been created for list-tasks polling.
	assert.Empty(t, store.runs, "no run record should be created for list-tasks")
}

func TestRunListTasksWorkflow_WithTasks_NoRunRecord(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// list-tasks workflow that outputs one task
	hostCloche := `workflow "list-tasks" {
  host {}

  step fetch {
    run     = "echo '{\"id\":\"task-1\",\"status\":\"open\",\"title\":\"Fix bug\"}'"
    results = [success, fail]
  }
  fetch:success -> done
  fetch:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Dispatcher: &fakeDispatcher{runID: "test-run"},
		Store:      store,
	}

	tasks, result, err := RunListTasksWorkflow(context.Background(), runner, tmpDir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-1", tasks[0].ID)

	// No run record should be created for list-tasks — it's a polling operation.
	assert.Empty(t, store.runs, "no run record should be created for list-tasks")
	assert.NotEmpty(t, result.RunID, "RunID should still be set for output directory resolution")
}

// TestHostStatusHandler_ReadsStepLogFile verifies that OnStepComplete reads
// the .log file so that step output is logged and broadcast.
func TestHostStatusHandler_ReadsStepLogFile(t *testing.T) {
	outputDir := t.TempDir()

	// Write a .log file (as the host executor does)
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"), []byte("build output data\n"), 0644))

	handler := &hostStatusHandler{
		outputDir: outputDir,
	}

	step := &domain.Step{Name: "build"}
	handler.OnStepComplete(nil, step, "success")

	// Verify only one .log file exists (no duplicate .out file)
	entries, err := os.ReadDir(outputDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only one file should exist in output dir")
	assert.Equal(t, "build.log", entries[0].Name())
}

// TestHostStatusHandler_NoLogFileWhenNoOutput verifies that OnStepComplete
// does not create a .log file when there is no output file.
func TestHostStatusHandler_NoLogFileWhenNoOutput(t *testing.T) {
	outputDir := t.TempDir()

	handler := &hostStatusHandler{
		outputDir: outputDir,
	}

	step := &domain.Step{Name: "missing"}
	handler.OnStepComplete(nil, step, "success")

	// No .log file should be created
	_, err := os.Stat(filepath.Join(outputDir, "missing.log"))
	assert.True(t, os.IsNotExist(err), ".log file should not be created when .out doesn't exist")
}
