package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore returns a predetermined run on GetRun.
type fakeStore struct {
	mu       sync.Mutex
	runs     map[string]*domain.Run
	kvData   map[string]string
	attempts map[string]*domain.Attempt
}

func (f *fakeStore) CreateRun(_ context.Context, run *domain.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[run.ID] = run
	return nil
}
func (f *fakeStore) GetRun(_ context.Context, id string) (*domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[id]; ok {
		return r, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeStore) GetRunByAttempt(_ context.Context, attemptID, id string) (*domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[id]; ok && r.AttemptID == attemptID {
		return r, nil
	}
	return nil, os.ErrNotExist
}
func (f *fakeStore) UpdateRun(_ context.Context, run *domain.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[run.ID] = run
	return nil
}
func (f *fakeStore) DeleteRun(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
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
func (f *fakeStore) GetContextKey(_ context.Context, taskID, attemptID, runID, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvData == nil {
		return "", false, nil
	}
	v, ok := f.kvData[taskID+"/"+attemptID+"/"+runID+"/"+key]
	return v, ok, nil
}
func (f *fakeStore) SetContextKey(_ context.Context, taskID, attemptID, runID, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kvData == nil {
		f.kvData = make(map[string]string)
	}
	f.kvData[taskID+"/"+attemptID+"/"+runID+"/"+key] = value
	return nil
}
func (f *fakeStore) ListContextKeys(_ context.Context, taskID, attemptID, runID string) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) DeleteContextKeys(_ context.Context, taskID, attemptID string) error {
	return nil
}
func (f *fakeStore) SaveAttempt(_ context.Context, a *domain.Attempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attempts == nil {
		f.attempts = make(map[string]*domain.Attempt)
	}
	cp := *a
	f.attempts[a.ID] = &cp
	return nil
}
func (f *fakeStore) GetAttempt(_ context.Context, id string) (*domain.Attempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attempts == nil {
		return nil, fmt.Errorf("attempt %q not found", id)
	}
	a, ok := f.attempts[id]
	if !ok {
		return nil, fmt.Errorf("attempt %q not found", id)
	}
	cp := *a
	return &cp, nil
}
func (f *fakeStore) ListAttempts(_ context.Context, taskID string) ([]*domain.Attempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Attempt
	for _, a := range f.attempts {
		if a.TaskID == taskID {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (f *fakeStore) FailStaleAttempts(_ context.Context) (int64, error) {
	return 0, nil
}

func (f *fakeStore) countAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.attempts)
}

func (f *fakeStore) allAttempts() []*domain.Attempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Attempt, 0, len(f.attempts))
	for _, a := range f.attempts {
		cp := *a
		out = append(out, &cp)
	}
	return out
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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "fail", result.Result)
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
	assert.Equal(t, "custom", result.Result)

	// Marker line should be stripped from output
	data, err := os.ReadFile(filepath.Join(outputDir, "marker.log"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "CLOCHE_RESULT")
	assert.Contains(t, string(data), "some output")
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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)
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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "fail", result.Result)
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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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
	assert.Equal(t, "success", result.Result)

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

workflow "cleanup" {
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

	// Run cleanup workflow
	result, err = runner.RunNamed(context.Background(), tmpDir, "cleanup")
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
			"MY_CUSTOM_VAR=hello",
			"MY_RUN_REF=main-run-123",
		},
	}

	step := &domain.Step{
		Name:    "check-env",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo \"CUSTOM=$MY_CUSTOM_VAR REF=$MY_RUN_REF\""},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)

	data, err := os.ReadFile(filepath.Join(outputDir, "check-env.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "CUSTOM=hello")
	assert.Contains(t, string(data), "REF=main-run-123")
}

func TestRunner_ExtraEnv_Propagated(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "post-merge" {
  host {}

  step check {
    run     = "echo CUSTOM=$MY_CUSTOM_VAR"
    results = [success, fail]
  }
  check:success -> done
  check:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{
		Store:      store,
		ExtraEnv:   []string{"MY_CUSTOM_VAR=hello"},
	}

	result, err := runner.RunNamed(context.Background(), tmpDir, "post-merge")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// Verify the env var was available to the script
	data, err := os.ReadFile(filepath.Join(result.OutputDir, "check.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "CUSTOM=hello")
}

func TestRunner_ExtraEnv_RestoredOnResume(t *testing.T) {
	tmpDir := t.TempDir()

	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow "post-merge" {
  host {}

  step route {
    run     = "echo routed"
    results = [success, fail]
  }
  step merge {
    run     = "echo REF=$MY_RUN_REF"
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
		Store:      store,
		ExtraEnv:   []string{"MY_RUN_REF=develop-original-branch"},
	}

	// Run the workflow — it succeeds and persists ExtraEnv to context.json
	result, err := runner.RunNamed(context.Background(), tmpDir, "post-merge")
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
		Store:      store,
	}

	resumeResult, err := resumeRunner.ResumeRun(context.Background(), run, "merge")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, resumeResult.State)

	// Verify the merge step got MY_RUN_REF from restored ExtraEnv
	data, err := os.ReadFile(filepath.Join(resumeResult.OutputDir, "merge.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "REF=develop-original-branch")
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
	handler.OnStepComplete(nil, step, "success", nil)

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
	handler.OnStepComplete(nil, step, "success", nil)

	// No .log file should be created
	_, err := os.Stat(filepath.Join(outputDir, "missing.log"))
	assert.True(t, os.IsNotExist(err), ".log file should not be created when .out doesn't exist")
}

// --- Auto-context seeding tests ---

func TestExecutor_SeedsRunContext_OnFirstUse(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	store := &fakeStore{}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		TaskID:       "task-seed-test",
		AttemptID:    "attempt-1",
		WorkflowName: "main",
		HostRunID:    "main-run-001",
		Store:        store,
	}

	step := &domain.Step{
		Name:    "greet",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hi"},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	// Run-level keys should be seeded.
	taskID, ok, err := store.GetContextKey(context.Background(), "task-seed-test", "attempt-1", "main-run-001", "task_id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "task-seed-test", taskID)

	attemptID, ok, err := store.GetContextKey(context.Background(), "task-seed-test", "attempt-1", "main-run-001", "attempt_id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "attempt-1", attemptID)

	workflow, ok, err := store.GetContextKey(context.Background(), "task-seed-test", "attempt-1", "main-run-001", "workflow")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "main", workflow)

	runID, ok, err := store.GetContextKey(context.Background(), "task-seed-test", "attempt-1", "main-run-001", "run_id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "main-run-001", runID)
}

func TestExecutor_SeedsRunContext_OnlyOnce(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	store := &fakeStore{}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		TaskID:       "task-once-test",
		AttemptID:    "attempt-1",
		WorkflowName: "main",
		HostRunID:    "main-run-001",
		Store:        store,
	}

	step := &domain.Step{
		Name:    "step-a",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hi"},
	}

	// Execute twice to verify seedOnce fires only once.
	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	// Overwrite run_id key manually to detect re-seeding (run_id is only set in seedOnce).
	require.NoError(t, store.SetContextKey(context.Background(), "task-once-test", "attempt-1", "main-run-001", "run_id", "overwritten"))

	step2 := &domain.Step{
		Name:    "step-b",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo bye"},
	}
	_, err = executor.Execute(context.Background(), step2)
	require.NoError(t, err)

	// Should still be "overwritten" — seedOnce did not run again.
	val, ok, err := store.GetContextKey(context.Background(), "task-once-test", "attempt-1", "main-run-001", "run_id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "overwritten", val)
}

func TestExecutor_SetsStepResult_AfterExecution(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	store := &fakeStore{}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		TaskID:       "task-result-test",
		WorkflowName: "main",
		HostRunID:    "main-run-001",
		Store:        store,
	}

	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo built"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)

	// Step result key should be set.
	val, ok, err := store.GetContextKey(context.Background(), "task-result-test", "", "main-run-001", "main:build:result")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "success", val)
}

func TestExecutor_SetsPrevStep_FromContext(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	store := &fakeStore{}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		TaskID:       "task-prev-test",
		WorkflowName: "main",
		HostRunID:    "main-run-001",
		Store:        store,
	}

	step := &domain.Step{
		Name:    "deploy",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo deploying"},
	}

	ctx := engine.WithStepTrigger(context.Background(), engine.StepTrigger{
		PrevStep:   "build",
		PrevResult: "success",
	})

	_, err := executor.Execute(ctx, step)
	require.NoError(t, err)

	prevStep, ok, err := store.GetContextKey(context.Background(), "task-prev-test", "", "main-run-001", "prev_step")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "build", prevStep)

	prevResult, ok, err := store.GetContextKey(context.Background(), "task-prev-test", "", "main-run-001", "prev_step_exit")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "success", prevResult)
}

func TestExecutor_SkipsContextSeeding_WhenNoTaskID(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		WorkflowName: "main",
		HostRunID:    "main-run-001",
		// No TaskID
	}

	step := &domain.Step{
		Name:    "greet",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hi"},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)

	// No context.json should be created when TaskID is empty.
	contextPath := filepath.Join(tmpDir, ".cloche", "runs", "", "context.json")
	_, statErr := os.Stat(contextPath)
	assert.True(t, os.IsNotExist(statErr), "context.json should not be created without a task ID")
}

// fakeHumanPollStore is an in-memory implementation of ports.HumanPollStore for tests.
type fakeHumanPollStore struct {
	mu      sync.Mutex
	records map[string]*ports.HumanPollRecord // key: runID+"/"+stepName
}

func (f *fakeHumanPollStore) key(runID, stepName string) string {
	return runID + "/" + stepName
}

func (f *fakeHumanPollStore) UpsertHumanPoll(_ context.Context, r *ports.HumanPollRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *r
	f.records[f.key(r.RunID, r.StepName)] = &cp
	return nil
}

func (f *fakeHumanPollStore) GetHumanPoll(_ context.Context, runID, stepName string) (*ports.HumanPollRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[f.key(runID, stepName)]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeHumanPollStore) DeleteHumanPoll(_ context.Context, runID, stepName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, f.key(runID, stepName))
	return nil
}

func (f *fakeHumanPollStore) ListHumanPolls(_ context.Context, runID string) ([]*ports.HumanPollRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*ports.HumanPollRecord
	for _, r := range f.records {
		if r.RunID == runID {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

func newFakeHumanPollStore() *fakeHumanPollStore {
	return &fakeHumanPollStore{records: make(map[string]*ports.HumanPollRecord)}
}

// TestExecutor_HumanStep_ImmediateDecision checks that a human step that
// returns a wire result on the first poll completes immediately.
func TestExecutor_HumanStep_ImmediateDecision(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	pollStore := newFakeHumanPollStore()

	executor := &Executor{
		ProjectDir:     tmpDir,
		OutputDir:      outputDir,
		HostRunID:      "run-1",
		HumanPollStore: pollStore,
	}

	// Script outputs a CLOCHE_RESULT wire on first invocation.
	step := &domain.Step{
		Name:    "code-review",
		Type:    domain.StepTypeHuman,
		Results: []string{"approved", "fix", "timeout"},
		Config: map[string]string{
			"script":   "echo 'CLOCHE_RESULT:approved'",
			"interval": "10s",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "approved", result.Result)

	// Poll record should be cleaned up after completion.
	record, _ := pollStore.GetHumanPoll(context.Background(), "run-1", "code-review")
	assert.Nil(t, record, "poll record should be deleted after step completion")
}

// TestExecutor_HumanStep_PendingThenDecision verifies that a human step
// keeps polling when the script exits 0 with no wire, then resolves when
// the script outputs a wire result.
func TestExecutor_HumanStep_PendingThenDecision(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	// Write a counter file so the script can change behavior after first call.
	counterFile := filepath.Join(tmpDir, "count.txt")
	require.NoError(t, os.WriteFile(counterFile, []byte("0"), 0644))

	scriptPath := filepath.Join(tmpDir, "poll.sh")
	scriptContent := `#!/bin/sh
count=$(cat ` + counterFile + `)
count=$((count + 1))
echo $count > ` + counterFile + `
if [ $count -ge 2 ]; then
  echo 'CLOCHE_RESULT:approved'
fi
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
		HostRunID:  "run-2",
	}

	step := &domain.Step{
		Name:    "gate",
		Type:    domain.StepTypeHuman,
		Results: []string{"approved", "timeout"},
		Config: map[string]string{
			"script":   "sh " + scriptPath,
			"interval": "50ms", // fast for testing
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "approved", result.Result)
}

// TestExecutor_HumanStep_ScriptFailure verifies that a non-zero exit with no
// wire output causes the step to return "fail".
func TestExecutor_HumanStep_ScriptFailure(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	step := &domain.Step{
		Name:    "bad-poll",
		Type:    domain.StepTypeHuman,
		Results: []string{"approved", "fail"},
		Config: map[string]string{
			"script":   "exit 1",
			"interval": "10s",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "fail", result.Result)
}

// TestExecutor_HumanStep_ContextTimeout verifies that the step returns
// "timeout" when the context deadline is exceeded while polling.
func TestExecutor_HumanStep_ContextTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	// Script always exits 0 with no wire (pending forever).
	step := &domain.Step{
		Name:    "wait-forever",
		Type:    domain.StepTypeHuman,
		Results: []string{"approved", "timeout"},
		Config: map[string]string{
			"script":   "exit 0",
			"interval": "50ms",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := executor.Execute(ctx, step)
	require.NoError(t, err)
	assert.Equal(t, "timeout", result.Result)
}

// TestExecutor_HumanStep_PollStoreTracking verifies that poll records are
// written to the HumanPollStore during execution.
func TestExecutor_HumanStep_PollStoreTracking(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	pollStore := newFakeHumanPollStore()

	counterFile := filepath.Join(tmpDir, "count.txt")
	require.NoError(t, os.WriteFile(counterFile, []byte("0"), 0644))

	scriptPath := filepath.Join(tmpDir, "poll.sh")
	scriptContent := `#!/bin/sh
count=$(cat ` + counterFile + `)
count=$((count + 1))
echo $count > ` + counterFile + `
if [ $count -ge 3 ]; then
  echo 'CLOCHE_RESULT:approved'
fi
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	executor := &Executor{
		ProjectDir:     tmpDir,
		OutputDir:      outputDir,
		HostRunID:      "run-track",
		HumanPollStore: pollStore,
	}

	step := &domain.Step{
		Name:    "review",
		Type:    domain.StepTypeHuman,
		Results: []string{"approved"},
		Config: map[string]string{
			"script":   "sh " + scriptPath,
			"interval": "50ms",
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "approved", result.Result)

	// Record deleted after completion.
	record, _ := pollStore.GetHumanPoll(context.Background(), "run-track", "review")
	assert.Nil(t, record)
}

// TestEngine_HumanStep_Timeout verifies that the engine handles the timeout
// wire when a human step's context times out.
func TestEngine_HumanStep_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	executor := &Executor{
		ProjectDir: tmpDir,
		OutputDir:  outputDir,
	}

	// Create a workflow where the human step wires timeout → done.
	wf := &domain.Workflow{
		Name:      "review",
		Location:  domain.LocationHost,
		EntryStep: "gate",
		Steps: map[string]*domain.Step{
			"gate": {
				Name:    "gate",
				Type:    domain.StepTypeHuman,
				Results: []string{"approved", "timeout"},
				Config: map[string]string{
					"script":   "exit 0",
					"interval": "50ms",
					"timeout":  "200ms",
				},
			},
		},
		Wiring: []domain.Wire{
			{From: "gate", Result: "approved", To: domain.StepDone},
			{From: "gate", Result: "timeout", To: domain.StepDone},
		},
	}

	eng := engine.New(executor)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
}
