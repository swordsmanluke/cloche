# Self-Evolution System Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement a daemon-side post-run evolution pipeline that analyzes workflow history to automatically improve prompts, add validation steps, and refine workflows.

**Architecture:** Multi-stage LLM pipeline (Collector → Classifier → Reflector → Executor branches) running in the daemon after each workflow completion. Three executor branches: Prompt Curator (ACE-style), Script Generator (LLM code gen), DSL Mutator (deterministic Go). Changes auto-applied with JSONL audit trail and snapshots.

**Tech Stack:** Go, SQLite, gRPC, TOML config (`github.com/BurntSushi/toml`), existing LLM agent invocation pattern.

**Design doc:** `docs/plans/2026-02-24-self-evolution-design.md`

---

## Task 1: Extend Domain Types

**Files:**
- Modify: `internal/domain/run.go`
- Modify: `internal/domain/workflow.go`
- Test: `internal/domain/run_test.go`

**Step 1: Write failing test for Run.ProjectDir**

Add to `internal/domain/run_test.go`:

```go
func TestRunProjectDir(t *testing.T) {
	r := NewRun("test-1", "develop")
	r.ProjectDir = "/home/user/project"
	assert.Equal(t, "/home/user/project", r.ProjectDir)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/ -run TestRunProjectDir -v`
Expected: FAIL — `r.ProjectDir undefined`

**Step 3: Add ProjectDir to Run**

In `internal/domain/run.go`, add `ProjectDir string` to the `Run` struct.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/ -run TestRunProjectDir -v`
Expected: PASS

**Step 5: Write failing test for StepExecution new fields**

Add to `internal/domain/run_test.go`:

```go
func TestStepExecutionCapturedData(t *testing.T) {
	exec := &StepExecution{
		StepName:      "implement",
		PromptText:    "Write a hello world",
		AgentOutput:   "Here is the code...",
		AttemptNumber: 2,
	}
	assert.Equal(t, "Write a hello world", exec.PromptText)
	assert.Equal(t, "Here is the code...", exec.AgentOutput)
	assert.Equal(t, 2, exec.AttemptNumber)
}
```

**Step 6: Run test to verify it fails**

Run: `go test ./internal/domain/ -run TestStepExecutionCapturedData -v`
Expected: FAIL — fields undefined

**Step 7: Add fields to StepExecution**

In `internal/domain/run.go`, add to `StepExecution`:
```go
PromptText    string
AgentOutput   string
AttemptNumber int
```

**Step 8: Run all domain tests**

Run: `go test ./internal/domain/ -v`
Expected: ALL PASS

**Step 9: Commit**

```bash
git add internal/domain/
git commit -m "feat(domain): add ProjectDir to Run, capture fields to StepExecution"
```

---

## Task 2: SQLite Schema Migration

**Files:**
- Modify: `internal/adapters/sqlite/store.go`
- Test: `internal/adapters/sqlite/store_test.go`

The current `migrate()` function uses `CREATE TABLE IF NOT EXISTS`. We need to add
columns to existing tables and a new `evolution_log` table. Use `ALTER TABLE` with
error suppression (SQLite returns error if column already exists — catch and ignore).

**Step 1: Write failing test for project_dir persistence**

Add to `internal/adapters/sqlite/store_test.go`:

```go
func TestRunProjectDir(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)

	run := domain.NewRun("test-1", "develop")
	run.ProjectDir = "/home/user/project"
	run.Start()

	err = store.CreateRun(context.Background(), run)
	require.NoError(t, err)

	got, err := store.GetRun(context.Background(), "test-1")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/project", got.ProjectDir)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/sqlite/ -run TestRunProjectDir -v`
Expected: FAIL — column doesn't exist or field not scanned

**Step 3: Update schema and CRUD methods**

In `internal/adapters/sqlite/store.go`:

1. In `migrate()`, after existing `CREATE TABLE` statements, add:
```go
// Schema evolution - add columns (ignore errors if already exist)
alterStatements := []string{
    `ALTER TABLE runs ADD COLUMN project_dir TEXT NOT NULL DEFAULT ''`,
    `ALTER TABLE step_executions ADD COLUMN prompt_text TEXT`,
    `ALTER TABLE step_executions ADD COLUMN agent_output TEXT`,
    `ALTER TABLE step_executions ADD COLUMN attempt_number INTEGER DEFAULT 0`,
}
for _, stmt := range alterStatements {
    db.Exec(stmt) // ignore "duplicate column" errors
}

// New tables
db.Exec(`CREATE TABLE IF NOT EXISTS evolution_log (
    id TEXT PRIMARY KEY,
    project_dir TEXT NOT NULL,
    workflow_name TEXT NOT NULL,
    trigger_run_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    classification TEXT,
    changes_json TEXT NOT NULL,
    knowledge_delta TEXT
)`)
```

2. Update `CreateRun` to include `project_dir` in INSERT
3. Update `GetRun` to scan `project_dir`
4. Update `ListRuns` to scan `project_dir`
5. Update `SaveCapture` to include `prompt_text`, `agent_output`, `attempt_number`
6. Update `GetCaptures` to scan the new fields

**Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/sqlite/ -run TestRunProjectDir -v`
Expected: PASS

**Step 5: Write test for captured data round-trip**

Add to `internal/adapters/sqlite/store_test.go`:

```go
func TestCaptureWithPromptAndOutput(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)

	run := domain.NewRun("test-1", "develop")
	run.Start()
	store.CreateRun(context.Background(), run)

	exec := &domain.StepExecution{
		StepName:      "implement",
		PromptText:    "Write hello world",
		AgentOutput:   "Here is the code",
		AttemptNumber: 1,
		StartedAt:     time.Now(),
	}
	err = store.SaveCapture(context.Background(), "test-1", exec)
	require.NoError(t, err)

	caps, err := store.GetCaptures(context.Background(), "test-1")
	require.NoError(t, err)
	require.Len(t, caps, 1)
	assert.Equal(t, "Write hello world", caps[0].PromptText)
	assert.Equal(t, "Here is the code", caps[0].AgentOutput)
	assert.Equal(t, 1, caps[0].AttemptNumber)
}
```

**Step 6: Run test**

Run: `go test ./internal/adapters/sqlite/ -run TestCaptureWithPromptAndOutput -v`
Expected: PASS (if step 3 was done correctly)

**Step 7: Run all store tests**

Run: `go test ./internal/adapters/sqlite/ -v`
Expected: ALL PASS

**Step 8: Commit**

```bash
git add internal/adapters/sqlite/
git commit -m "feat(sqlite): add project_dir, prompt capture, and evolution_log schema"
```

---

## Task 3: Evolution Store Port and Implementation

**Files:**
- Modify: `internal/ports/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Test: `internal/adapters/sqlite/store_test.go`

**Step 1: Define EvolutionStore interface**

Add to `internal/ports/store.go`:

```go
type EvolutionEntry struct {
	ID             string
	ProjectDir     string
	WorkflowName   string
	TriggerRunID   string
	CreatedAt      time.Time
	Classification string
	ChangesJSON    string
	KnowledgeDelta string
}

type EvolutionStore interface {
	SaveEvolution(ctx context.Context, entry *EvolutionEntry) error
	GetLastEvolution(ctx context.Context, projectDir, workflowName string) (*EvolutionEntry, error)
	ListRunsSince(ctx context.Context, projectDir, workflowName, sinceRunID string) ([]*domain.Run, error)
}
```

**Step 2: Write failing test for ListRunsSince**

Add to `internal/adapters/sqlite/store_test.go`:

```go
func TestListRunsSince(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	ctx := context.Background()

	// Create 3 runs for the same project+workflow
	for i, id := range []string{"run-1", "run-2", "run-3"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/project"
		r.Start()
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		require.NoError(t, store.CreateRun(ctx, r))
	}

	// List runs since run-1 (should return run-2 and run-3)
	runs, err := store.ListRunsSince(ctx, "/project", "develop", "run-1")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/adapters/sqlite/ -run TestListRunsSince -v`
Expected: FAIL — method not found

**Step 4: Implement EvolutionStore on SQLite Store**

In `internal/adapters/sqlite/store.go`, add methods:

- `SaveEvolution` — INSERT into `evolution_log`
- `GetLastEvolution` — SELECT from `evolution_log` WHERE project_dir AND workflow_name ORDER BY created_at DESC LIMIT 1
- `ListRunsSince` — SELECT from `runs` WHERE project_dir AND workflow_name AND started_at > (SELECT started_at FROM runs WHERE id = sinceRunID). If sinceRunID is empty, return all runs for this project+workflow.

**Step 5: Run tests**

Run: `go test ./internal/adapters/sqlite/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/ports/store.go internal/adapters/sqlite/
git commit -m "feat(ports): add EvolutionStore interface and SQLite implementation"
```

---

## Task 4: Status Protocol Extensions

**Files:**
- Modify: `internal/protocol/status.go`
- Test: `internal/protocol/status_test.go`

**Step 1: Write failing test for new fields**

Add to `internal/protocol/status_test.go`:

```go
func TestStatusMessagePromptCapture(t *testing.T) {
	var buf bytes.Buffer
	w := NewStatusWriter(&buf)

	w.StepStartedWithPrompt("implement", "the assembled prompt")
	w.StepCompletedWithCapture("implement", "success", "agent output here", 2)

	msgs := ParseStatusStream(buf.Bytes())
	require.Len(t, msgs, 2)

	assert.Equal(t, "the assembled prompt", msgs[0].PromptText)
	assert.Equal(t, "agent output here", msgs[1].AgentOutput)
	assert.Equal(t, 2, msgs[1].AttemptNumber)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/protocol/ -run TestStatusMessagePromptCapture -v`
Expected: FAIL — fields/methods undefined

**Step 3: Extend StatusMessage and StatusWriter**

In `internal/protocol/status.go`:

1. Add fields to `StatusMessage`:
```go
PromptText    string `json:"prompt_text,omitempty"`
AgentOutput   string `json:"agent_output,omitempty"`
AttemptNumber int    `json:"attempt_number,omitempty"`
```

2. Add new methods to `StatusWriter`:
```go
func (w *StatusWriter) StepStartedWithPrompt(stepName, promptText string) {
    w.write(StatusMessage{
        Type:       MsgStepStarted,
        StepName:   stepName,
        PromptText: promptText,
        Timestamp:  time.Now(),
    })
}

func (w *StatusWriter) StepCompletedWithCapture(stepName, result, agentOutput string, attempt int) {
    w.write(StatusMessage{
        Type:          MsgStepCompleted,
        StepName:      stepName,
        Result:        result,
        AgentOutput:   agentOutput,
        AttemptNumber: attempt,
        Timestamp:     time.Now(),
    })
}
```

**Step 4: Run all protocol tests**

Run: `go test ./internal/protocol/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/protocol/
git commit -m "feat(protocol): add prompt/output capture fields to status messages"
```

---

## Task 5: Prompt Adapter Capture

**Files:**
- Modify: `internal/adapters/agents/prompt/prompt.go`
- Test: `internal/adapters/agents/prompt/prompt_test.go`

The prompt adapter needs to return captured data (assembled prompt and agent output)
alongside the result string. Currently `Execute` returns `(string, error)`. We need
to extend this without breaking the `ports.AgentAdapter` interface.

**Approach:** Add a `CapturedData` struct and a callback/channel mechanism. The
adapter stores captured data that the runner can retrieve after execution.

**Step 1: Write failing test for capture**

Add to `internal/adapters/agents/prompt/prompt_test.go`:

```go
func TestExecuteCapturesPromptAndOutput(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "prompt.txt"), []byte("user request"), 0644)

	// Use echo as the agent command - it will output the prompt it receives
	a := &Adapter{Command: "cat", Args: []string{}}
	a.OnCapture = func(c CapturedData) {
		assert.Contains(t, c.PromptText, "user request")
		assert.NotEmpty(t, c.AgentOutput)
		assert.Equal(t, 1, c.AttemptNumber)
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Build something"},
	}

	result, err := a.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/agents/prompt/ -run TestExecuteCapturesPromptAndOutput -v`
Expected: FAIL — `OnCapture` and `CapturedData` undefined

**Step 3: Add capture mechanism to Adapter**

In `internal/adapters/agents/prompt/prompt.go`:

1. Define:
```go
type CapturedData struct {
	PromptText    string
	AgentOutput   string
	AttemptNumber int
}
```

2. Add `OnCapture func(CapturedData)` field to `Adapter` struct.

3. In `Execute()`, after `assemblePrompt()`, capture the prompt text. After
   `cmd.Run()`, capture the stdout. Before returning, call `OnCapture` if set:
```go
if a.OnCapture != nil {
    a.OnCapture(CapturedData{
        PromptText:    fullPrompt,
        AgentOutput:   stdout.String(),
        AttemptNumber: readAttemptCount(workDir, step.Name),
    })
}
```

**Step 4: Run all prompt adapter tests**

Run: `go test ./internal/adapters/agents/prompt/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/adapters/agents/prompt/
git commit -m "feat(prompt): capture assembled prompt and agent output"
```

---

## Task 6: Wire Capture Through Runner to Status Protocol

**Files:**
- Modify: `internal/agent/runner.go`
- Test: `internal/agent/runner_test.go`

The runner creates the prompt adapter and the status writer. Wire the adapter's
`OnCapture` callback to emit the new status messages.

**Step 1: Write failing test**

Extend the existing runner test in `internal/agent/runner_test.go` to verify that
status messages include prompt and output capture for agent steps.

```go
func TestRunnerCapturesAgentData(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal workflow with an agent step that uses "cat" as the command
	wf := `workflow test {
		step echo {
			prompt = "hello"
			results = [success]
		}
		echo:success -> done
	}`
	wfPath := filepath.Join(dir, "test.cloche")
	os.WriteFile(wfPath, []byte(wf), 0644)
	os.MkdirAll(filepath.Join(dir, ".cloche"), 0755)

	var buf bytes.Buffer
	cfg := RunnerConfig{
		WorkflowPath: wfPath,
		WorkDir:      dir,
		StatusOutput: &buf,
	}

	r := NewRunner(cfg)
	// Override agent command to "cat" so it echoes the prompt
	os.Setenv("CLOCHE_AGENT_COMMAND", "cat")
	defer os.Unsetenv("CLOCHE_AGENT_COMMAND")

	err := r.Run(context.Background())
	require.NoError(t, err)

	msgs := protocol.ParseStatusStream(buf.Bytes())
	// Find step_started message — should have prompt_text
	var started *protocol.StatusMessage
	for i := range msgs {
		if msgs[i].Type == protocol.MsgStepStarted && msgs[i].StepName == "echo" {
			started = &msgs[i]
			break
		}
	}
	require.NotNil(t, started)
	assert.Contains(t, started.PromptText, "hello")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestRunnerCapturesAgentData -v`
Expected: FAIL — PromptText empty on status message

**Step 3: Wire capture callback in runner**

In `internal/agent/runner.go`, when creating the prompt adapter, set `OnCapture` to
emit status messages via the `StatusWriter`:

```go
promptAdapter.OnCapture = func(c prompt.CapturedData) {
    // The step_started message with prompt was already sent by the status handler.
    // Store captured data to be included in step_completed message.
    r.mu.Lock()
    r.captures[step.Name] = c
    r.mu.Unlock()
}
```

Alternatively, have the `statusReporter` use the new `StepStartedWithPrompt` and
`StepCompletedWithCapture` methods when captured data is available. The exact wiring
depends on whether the capture callback fires synchronously within `Execute()`.

The simpler approach: modify `statusReporter.OnStepStart` and `OnStepComplete` to
check for captured data. Since the prompt adapter's `Execute` is synchronous and the
status handler fires from the engine goroutine, store captured data on the adapter and
read it in the status handler.

**Step 4: Run all runner tests**

Run: `go test ./internal/agent/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/agent/
git commit -m "feat(runner): wire prompt/output capture to status protocol"
```

---

## Task 7: Daemon Capture Storage

**Files:**
- Modify: `internal/adapters/grpc/server.go`
- Test: `internal/adapters/grpc/server_test.go`

The daemon's `trackRun()` parses status messages and stores step executions. Update it
to extract and store the new fields.

**Step 1: Update trackRun to extract new fields**

In `internal/adapters/grpc/server.go`, in the `trackRun()` method where
`MsgStepStarted` and `MsgStepCompleted` are handled:

For `MsgStepStarted`: store `msg.PromptText` in the step execution.
For `MsgStepCompleted`: store `msg.AgentOutput` and `msg.AttemptNumber`.

The `SaveCapture` call already receives `*domain.StepExecution` — just populate the
new fields from the status message.

**Step 2: Update RunWorkflow to pass project_dir to run record**

In `RunWorkflow()`, after creating the run record, set:
```go
run.ProjectDir = req.ProjectDir
```

This is already available from the gRPC request.

**Step 3: Write test for end-to-end capture**

Extend `internal/adapters/grpc/server_test.go` to verify that after a run completes,
`GetCaptures()` returns step executions with prompt/output data populated.

**Step 4: Run all server tests**

Run: `go test ./internal/adapters/grpc/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/adapters/grpc/
git commit -m "feat(daemon): store captured prompt/output data from status messages"
```

---

## Task 8: Config System

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Add TOML dependency**

```bash
cd /home/lucas/workspace/cloche && go get github.com/BurntSushi/toml
```

**Step 2: Write failing test for config loading**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEvolutionConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config"), []byte(`
[evolution]
enabled = true
debounce_seconds = 45
min_confidence = "high"
max_prompt_bullets = 30
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, 45, cfg.Evolution.DebounceSeconds)
	assert.Equal(t, "high", cfg.Evolution.MinConfidence)
	assert.Equal(t, 30, cfg.Evolution.MaxPromptBullets)
}

func TestLoadEvolutionConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	// No config file — should return defaults

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, 30, cfg.Evolution.DebounceSeconds)
	assert.Equal(t, "medium", cfg.Evolution.MinConfidence)
	assert.Equal(t, 50, cfg.Evolution.MaxPromptBullets)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — package doesn't exist

**Step 4: Implement config loader**

Create `internal/config/config.go`:

```go
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type EvolutionConfig struct {
	Enabled          bool   `toml:"enabled"`
	DebounceSeconds  int    `toml:"debounce_seconds"`
	MinConfidence    string `toml:"min_confidence"`
	MaxPromptBullets int    `toml:"max_prompt_bullets"`
}

type Config struct {
	Evolution EvolutionConfig `toml:"evolution"`
}

func defaults() Config {
	return Config{
		Evolution: EvolutionConfig{
			Enabled:          true,
			DebounceSeconds:  30,
			MinConfidence:    "medium",
			MaxPromptBullets: 50,
		},
	}
}

func Load(projectDir string) (*Config, error) {
	cfg := defaults()
	path := filepath.Join(projectDir, ".cloche", "config")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
```

**Step 5: Run tests**

Run: `go test ./internal/config/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat(config): add TOML config loader with evolution settings"
```

---

## Task 9: Evolution Pipeline — Collector

**Files:**
- Create: `internal/evolution/collector.go`
- Create: `internal/evolution/collector_test.go`
- Create: `internal/evolution/types.go`

**Step 1: Define evolution types**

Create `internal/evolution/types.go`:

```go
package evolution

import "github.com/cloche-dev/cloche/internal/domain"

// CollectedData is the input to the Classifier and Reflector.
type CollectedData struct {
	Runs            []*domain.Run
	Captures        map[string][]*domain.StepExecution // run_id -> step executions
	KnowledgeBase   string                              // contents of knowledge/<workflow>.md
	CurrentPrompts  map[string]string                   // filename -> content
	CurrentWorkflow string                              // .cloche file content
	WorkflowPath    string                              // path to .cloche file
	ProjectDir      string
	WorkflowName    string
}

// Lesson is a structured insight extracted by the Reflector.
type Lesson struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"` // prompt_improvement, new_step
	StepType        string   `json:"step_type,omitempty"` // script, agent (for new_step)
	Target          string   `json:"target,omitempty"` // target file for prompt updates
	Insight         string   `json:"insight"`
	SuggestedAction string   `json:"suggested_action"`
	Evidence        []string `json:"evidence"` // run IDs
	Confidence      string   `json:"confidence"` // high, medium, low
}

// EvolutionResult records what an evolution pass produced.
type EvolutionResult struct {
	ID             string   `json:"id"`
	ProjectDir     string   `json:"project_dir"`
	WorkflowName   string   `json:"workflow_name"`
	TriggerRunID   string   `json:"trigger_run_id"`
	Classification string   `json:"classification"`
	Changes        []Change `json:"changes"`
	KnowledgeDelta string   `json:"knowledge_delta"`
}

// Change describes a single file modification made by evolution.
type Change struct {
	Type     string `json:"type"` // prompt_update, add_step, add_script
	File     string `json:"file"`
	Reason   string `json:"reason"`
	Snapshot string `json:"snapshot"`
}
```

**Step 2: Write failing test for Collector**

Create `internal/evolution/collector_test.go`:

```go
package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorGathersData(t *testing.T) {
	dir := t.TempDir()

	// Set up project structure
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"),
		[]byte("# Knowledge Base\n"), 0644)
	os.WriteFile(filepath.Join(dir, "prompts", "implement.md"),
		[]byte("Write good code"), 0644)
	os.WriteFile(filepath.Join(dir, "develop.cloche"),
		[]byte(`workflow develop { step impl { prompt = "test" results = [success] } impl:success -> done }`), 0644)

	c := &Collector{ProjectDir: dir, WorkflowName: "develop"}
	data, err := c.Collect(context.Background(), nil, nil) // nil store = no DB runs
	require.NoError(t, err)

	assert.Equal(t, "# Knowledge Base\n", data.KnowledgeBase)
	assert.Contains(t, data.CurrentPrompts, "prompts/implement.md")
	assert.NotEmpty(t, data.CurrentWorkflow)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestCollectorGathersData -v`
Expected: FAIL — package/type doesn't exist

**Step 4: Implement Collector**

Create `internal/evolution/collector.go`:

The Collector reads:
1. Runs from SQLite via `EvolutionStore.ListRunsSince()`
2. Step captures for each run via `CaptureStore.GetCaptures()`
3. Knowledge base from `.cloche/evolution/knowledge/<workflow>.md`
4. All prompt files referenced by the workflow (scan for `file("...")` patterns)
5. The workflow `.cloche` file itself

If store is nil (for testing without DB), skip DB reads.

**Step 5: Run tests**

Run: `go test ./internal/evolution/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Collector and core types"
```

---

## Task 10: Evolution Pipeline — Classifier

**Files:**
- Create: `internal/evolution/classifier.go`
- Create: `internal/evolution/classifier_test.go`

**Step 1: Define LLM interface for evolution**

Add to `internal/evolution/types.go`:

```go
// LLMClient abstracts LLM calls for evolution stages.
type LLMClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

**Step 2: Write failing test for Classifier**

Create `internal/evolution/classifier_test.go`:

```go
package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLM struct {
	response string
}

func (f *fakeLLM) Complete(ctx context.Context, system, user string) (string, error) {
	return f.response, nil
}

func TestClassifierCategorizesRun(t *testing.T) {
	llm := &fakeLLM{response: `{"classification": "bug"}`}
	c := &Classifier{LLM: llm}

	result, err := c.Classify(context.Background(), "Fix XSS vulnerability in login form")
	require.NoError(t, err)
	assert.Equal(t, "bug", result)
}

func TestClassifierDefaultsToFeature(t *testing.T) {
	llm := &fakeLLM{response: `{"classification": "feature"}`}
	c := &Classifier{LLM: llm}

	result, err := c.Classify(context.Background(), "Add user profile page")
	require.NoError(t, err)
	assert.Equal(t, "feature", result)
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestClassifier -v`
Expected: FAIL — Classifier undefined

**Step 4: Implement Classifier**

Create `internal/evolution/classifier.go`:

The Classifier sends the run prompt to the LLM with a system prompt asking it to
classify into one of: bug, feedback, feature, enhancement, chore. Returns JSON
with a `classification` field. Parse the JSON and return the classification string.
Default to "feature" if parsing fails.

**Step 5: Run tests**

Run: `go test ./internal/evolution/ -run TestClassifier -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Classifier stage"
```

---

## Task 11: Evolution Pipeline — Reflector

**Files:**
- Create: `internal/evolution/reflector.go`
- Create: `internal/evolution/reflector_test.go`

**Step 1: Write failing test for Reflector**

Create `internal/evolution/reflector_test.go`:

```go
package evolution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReflectorExtractsLessons(t *testing.T) {
	lessonsJSON, _ := json.Marshal(map[string]any{
		"lessons": []map[string]any{
			{
				"id":               "lesson-001",
				"category":         "prompt_improvement",
				"target":           "prompts/implement.md",
				"insight":          "Agent produces unsanitized HTML",
				"suggested_action": "Add sanitization rule",
				"evidence":         []string{"run-1", "run-2"},
				"confidence":       "high",
			},
		},
	})

	llm := &fakeLLM{response: string(lessonsJSON)}
	r := &Reflector{LLM: llm, MinConfidence: "medium"}

	data := &CollectedData{
		WorkflowName: "develop",
		KnowledgeBase: "# Knowledge Base\n",
	}

	lessons, err := r.Reflect(context.Background(), data, "bug")
	require.NoError(t, err)
	require.Len(t, lessons, 1)
	assert.Equal(t, "prompt_improvement", lessons[0].Category)
	assert.Equal(t, "high", lessons[0].Confidence)
}

func TestReflectorFiltersLowConfidence(t *testing.T) {
	lessonsJSON, _ := json.Marshal(map[string]any{
		"lessons": []map[string]any{
			{
				"id":         "lesson-001",
				"category":   "new_step",
				"confidence": "low",
			},
		},
	})

	llm := &fakeLLM{response: string(lessonsJSON)}
	r := &Reflector{LLM: llm, MinConfidence: "medium"}

	lessons, err := r.Reflect(context.Background(), &CollectedData{}, "bug")
	require.NoError(t, err)
	assert.Len(t, lessons, 0) // low confidence filtered out
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestReflector -v`
Expected: FAIL — Reflector undefined

**Step 3: Implement Reflector**

Create `internal/evolution/reflector.go`:

The Reflector sends the collected data (run results, failure logs, knowledge base,
current prompts, workflow) to the LLM with an ACE-style system prompt asking it to:
1. Examine execution traces for patterns
2. Extract structured lessons as JSON
3. Assign confidence levels based on evidence count

Parse the response as `{"lessons": [...]}`. Filter by `MinConfidence` (confidence
ordering: low < medium < high). Return the filtered lessons.

**Step 4: Run tests**

Run: `go test ./internal/evolution/ -run TestReflector -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Reflector stage with confidence filtering"
```

---

## Task 12: Prompt Curator (Branch A)

**Files:**
- Create: `internal/evolution/curator.go`
- Create: `internal/evolution/curator_test.go`

**Step 1: Write failing test for Prompt Curator**

Create `internal/evolution/curator_test.go`:

```go
package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCuratorUpdatesPrompt(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.WriteFile(promptPath, []byte("# Implementation Prompt\n\nWrite good code.\n"), 0644)

	updatedContent := "# Implementation Prompt\n\nWrite good code.\n\n## Learned Rules\n\n- Always sanitize user inputs with html.EscapeString()\n"
	llm := &fakeLLM{response: updatedContent}

	c := &Curator{LLM: llm}
	lesson := &Lesson{
		ID:              "lesson-001",
		Category:        "prompt_improvement",
		Target:          "prompts/implement.md",
		SuggestedAction: "Add sanitization rule",
	}

	change, err := c.Apply(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "prompt_update", change.Type)
	assert.Equal(t, "prompts/implement.md", change.File)

	// Verify file was updated
	content, _ := os.ReadFile(promptPath)
	assert.Contains(t, string(content), "sanitize user inputs")
}

func TestCuratorCreatesSnapshot(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompts", "implement.md")
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.WriteFile(promptPath, []byte("original content"), 0644)

	llm := &fakeLLM{response: "updated content"}
	c := &Curator{LLM: llm}

	change, err := c.Apply(context.Background(), dir, &Lesson{
		Category: "prompt_improvement",
		Target:   "prompts/implement.md",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, change.Snapshot)

	// Verify snapshot exists
	snapshotPath := filepath.Join(dir, ".cloche", "evolution", "snapshots", change.Snapshot)
	snapshot, err := os.ReadFile(snapshotPath)
	require.NoError(t, err)
	assert.Equal(t, "original content", string(snapshot))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestCurator -v`
Expected: FAIL — Curator undefined

**Step 3: Implement Curator**

Create `internal/evolution/curator.go`:

The Curator:
1. Reads the current prompt file
2. Sends it to the LLM with the lesson, asking it to merge the lesson as a
   structured bullet/rule (ACE-style curation) while preserving existing content
3. Creates a snapshot of the original file in `.cloche/evolution/snapshots/`
4. Writes the LLM's response as the new file content
5. Returns a `Change` record

The LLM system prompt should instruct ACE-style curation: append as a structured
bullet if new, update in place if it refines an existing rule, deduplicate.

**Step 4: Run tests**

Run: `go test ./internal/evolution/ -run TestCurator -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Prompt Curator with snapshot support"
```

---

## Task 13: Script Generator (Branch B)

**Files:**
- Create: `internal/evolution/scriptgen.go`
- Create: `internal/evolution/scriptgen_test.go`

**Step 1: Write failing test**

Create `internal/evolution/scriptgen_test.go`:

```go
package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptGeneratorCreatesScript(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "scripts"), 0755)

	llm := &fakeLLM{response: `{"path": "scripts/security-scan.sh", "content": "#!/bin/bash\ngosec ./..."}`}
	g := &ScriptGenerator{LLM: llm}

	lesson := &Lesson{
		ID:              "lesson-001",
		Category:        "new_step",
		StepType:        "script",
		SuggestedAction: "Add gosec security scan",
	}

	result, err := g.Generate(context.Background(), dir, lesson)
	require.NoError(t, err)
	assert.Equal(t, "scripts/security-scan.sh", result.Path)

	content, err := os.ReadFile(filepath.Join(dir, "scripts", "security-scan.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "gosec")

	// Verify executable
	info, _ := os.Stat(filepath.Join(dir, "scripts", "security-scan.sh"))
	assert.NotZero(t, info.Mode()&0111)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestScriptGenerator -v`
Expected: FAIL — ScriptGenerator undefined

**Step 3: Implement ScriptGenerator**

Create `internal/evolution/scriptgen.go`:

The ScriptGenerator:
1. Sends the lesson to the LLM asking it to generate a checker script
2. Parses the response as JSON with `path` and `content` fields
3. Creates parent directories if needed
4. Writes the script file with executable permissions (0755)
5. For agent-type steps, generates a prompt file instead

**Step 4: Run tests**

Run: `go test ./internal/evolution/ -run TestScriptGenerator -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Script Generator for new checker steps"
```

---

## Task 14: DSL Mutator (Branch C)

**Files:**
- Create: `internal/dsl/mutator.go`
- Create: `internal/dsl/mutator_test.go`

This is the deterministic Go code that adds steps, wiring, and collect conditions
to `.cloche` workflow files. Uses text-level manipulation (regex-based insertion at
known structural positions) rather than a full round-trip AST, to keep scope manageable.
Comments are preserved because we insert at boundaries rather than rewriting.

**Step 1: Write failing test for AddStep**

Create `internal/dsl/mutator_test.go`:

```go
package dsl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMutatorAddStep(t *testing.T) {
	input := `workflow develop {
  step test {
    run = "make test"
    results = [success, fail]
  }

  test:success -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.AddStep(input, StepDef{
		Name:    "security-scan",
		Type:    "script",
		Config:  map[string]string{"run": `"gosec ./..."`},
		Results: []string{"success", "fail"},
	})
	require.NoError(t, err)
	assert.Contains(t, result, "step security-scan")
	assert.Contains(t, result, `run = "gosec ./..."`)
	assert.Contains(t, result, "results = [success, fail]")

	// Verify the result still parses
	wf, err := Parse(result)
	require.NoError(t, err)
	assert.Contains(t, wf.Steps, "security-scan")
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/dsl/ -run TestMutatorAddStep -v`
Expected: FAIL — Mutator undefined

**Step 3: Implement AddStep**

Create `internal/dsl/mutator.go`:

```go
package dsl

import (
	"fmt"
	"regexp"
	"strings"
)

type StepDef struct {
	Name    string
	Type    string // "script" or "agent"
	Config  map[string]string
	Results []string
}

type WireDef struct {
	From   string
	Result string
	To     string
}

type CollectAddition struct {
	CollectTarget string
	Step          string
	Result        string
}

type Mutator struct{}
```

The `AddStep` method:
1. Finds the last `step ... { ... }` block in the file (regex for closing brace
   pattern)
2. Inserts the new step definition after it
3. Serializes the step as DSL text with proper indentation
4. Validates the result by calling `Parse()`

**Step 4: Write failing test for AddWiring**

```go
func TestMutatorAddWiring(t *testing.T) {
	input := `workflow develop {
  step test {
    run = "make test"
    results = [success, fail]
  }

  step scan {
    run = "gosec ./..."
    results = [success, fail]
  }

  test:success -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.AddWiring(input, []WireDef{
		{From: "test", Result: "success", To: "scan"},
		{From: "scan", Result: "fail", To: "abort"},
		{From: "scan", Result: "success", To: "done"},
	})
	require.NoError(t, err)
	assert.Contains(t, result, "test:success -> scan")
	assert.Contains(t, result, "scan:fail -> abort")
	assert.Contains(t, result, "scan:success -> done")

	wf, err := Parse(result)
	require.NoError(t, err)
	assert.True(t, len(wf.Wiring) > 2)
}
```

**Step 5: Implement AddWiring**

The `AddWiring` method:
1. Finds the last wiring line or collect statement (before the closing `}`)
2. Inserts new wire lines after the existing wiring section
3. Validates the result by calling `Parse()`

**Step 6: Write failing test for UpdateCollect**

```go
func TestMutatorUpdateCollect(t *testing.T) {
	input := `workflow develop {
  step test {
    run = "make test"
    results = [success, fail]
  }

  step lint {
    run = "golint ./..."
    results = [success, fail]
  }

  step scan {
    run = "gosec ./..."
    results = [success, fail]
  }

  test:success -> lint
  test:success -> scan
  collect all(lint:success, scan:success) -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.UpdateCollect(input, CollectAddition{
		CollectTarget: "done",
		Step:          "fmt",
		Result:        "success",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "fmt:success")

	wf, err := Parse(result)
	require.NoError(t, err)
	require.Len(t, wf.Collects, 1)
	assert.Len(t, wf.Collects[0].Conditions, 3)
}
```

**Step 7: Implement UpdateCollect**

The `UpdateCollect` method:
1. Finds the `collect all(...) -> target` line matching the target
2. Inserts the new condition before the closing `)`
3. Validates the result by calling `Parse()`

**Step 8: Run all mutator tests**

Run: `go test ./internal/dsl/ -v`
Expected: ALL PASS (including existing parser tests)

**Step 9: Commit**

```bash
git add internal/dsl/
git commit -m "feat(dsl): add Mutator for additive workflow modifications"
```

---

## Task 15: Audit Logger

**Files:**
- Create: `internal/evolution/audit.go`
- Create: `internal/evolution/audit_test.go`

**Step 1: Write failing test for JSONL audit logging**

Create `internal/evolution/audit_test.go`:

```go
package evolution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLoggerAppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution"), 0755)

	logger := &AuditLogger{ProjectDir: dir}

	result1 := &EvolutionResult{
		ID:           "evo-1",
		WorkflowName: "develop",
		Changes:      []Change{{Type: "prompt_update", File: "prompts/impl.md"}},
	}
	err := logger.Log(result1)
	require.NoError(t, err)

	result2 := &EvolutionResult{
		ID:           "evo-2",
		WorkflowName: "develop",
		Changes:      []Change{{Type: "add_step", File: "develop.cloche"}},
	}
	err = logger.Log(result2)
	require.NoError(t, err)

	// Verify JSONL format — two lines, each valid JSON
	content, err := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "log.jsonl"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Len(t, lines, 2)

	var entry1 EvolutionResult
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	assert.Equal(t, "evo-1", entry1.ID)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestAuditLogger -v`
Expected: FAIL — AuditLogger undefined

**Step 3: Implement AuditLogger**

Create `internal/evolution/audit.go`:

The AuditLogger:
1. `Log(result)` — JSON-encodes the `EvolutionResult` and appends a line to
   `.cloche/evolution/log.jsonl` (open with `O_APPEND|O_CREATE|O_WRONLY`)
2. `Snapshot(projectDir, relativePath)` — copies file to
   `.cloche/evolution/snapshots/<timestamp>-<basename>`, returns snapshot filename
3. `UpdateKnowledge(projectDir, workflowName, lessons)` — appends formatted
   lessons to `knowledge/<workflow>.md`

**Step 4: Write test for knowledge base update**

```go
func TestAuditLoggerUpdatesKnowledge(t *testing.T) {
	dir := t.TempDir()
	kbDir := filepath.Join(dir, ".cloche", "evolution", "knowledge")
	os.MkdirAll(kbDir, 0755)
	os.WriteFile(filepath.Join(kbDir, "develop.md"), []byte("# Knowledge Base: develop\n"), 0644)

	logger := &AuditLogger{ProjectDir: dir}
	lessons := []Lesson{
		{
			ID:              "P001",
			Category:        "prompt_improvement",
			Insight:         "Always sanitize HTML inputs",
			SuggestedAction: "Add rule to implement prompt",
			Evidence:        []string{"run-1", "run-2"},
		},
	}

	err := logger.UpdateKnowledge("develop", lessons)
	require.NoError(t, err)

	content, _ := os.ReadFile(filepath.Join(kbDir, "develop.md"))
	assert.Contains(t, string(content), "[P001]")
	assert.Contains(t, string(content), "sanitize HTML")
}
```

**Step 5: Run all audit tests**

Run: `go test ./internal/evolution/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add AuditLogger with JSONL and knowledge base"
```

---

## Task 16: Evolution Orchestrator

**Files:**
- Create: `internal/evolution/orchestrator.go`
- Create: `internal/evolution/orchestrator_test.go`

This is the main pipeline that wires all stages together.

**Step 1: Write failing test for orchestrator**

Create `internal/evolution/orchestrator_test.go`:

```go
package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestratorEndToEnd(t *testing.T) {
	dir := t.TempDir()

	// Set up project structure
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "knowledge"), 0755)
	os.MkdirAll(filepath.Join(dir, ".cloche", "evolution", "snapshots"), 0755)
	os.MkdirAll(filepath.Join(dir, "prompts"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md"),
		[]byte("# Knowledge Base: develop\n"), 0644)
	os.WriteFile(filepath.Join(dir, "prompts", "implement.md"),
		[]byte("Write good code.\n"), 0644)
	os.WriteFile(filepath.Join(dir, "develop.cloche"),
		[]byte(`workflow develop {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  implement:success -> test
  test:success -> done
  test:fail -> abort
}`), 0644)

	// Fake LLM that returns appropriate responses per stage
	llm := &scriptedLLM{
		responses: []string{
			// Classifier
			`{"classification": "bug"}`,
			// Reflector
			`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": "prompts/implement.md", "insight": "XSS pattern", "suggested_action": "Add sanitization rule", "evidence": ["run-1"], "confidence": "high"}]}`,
			// Curator
			"Write good code.\n\n## Learned Rules\n\n- Always sanitize user inputs\n",
		},
	}

	orch := NewOrchestrator(OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "bug", result.Classification)
	require.Len(t, result.Changes, 1)
	assert.Equal(t, "prompt_update", result.Changes[0].Type)

	// Verify prompt was actually updated
	content, _ := os.ReadFile(filepath.Join(dir, "prompts", "implement.md"))
	assert.Contains(t, string(content), "sanitize user inputs")

	// Verify audit log was written
	logContent, _ := os.ReadFile(filepath.Join(dir, ".cloche", "evolution", "log.jsonl"))
	assert.Contains(t, string(logContent), "L001")
}

// scriptedLLM returns responses in order
type scriptedLLM struct {
	responses []string
	idx       int
}

func (s *scriptedLLM) Complete(ctx context.Context, system, user string) (string, error) {
	resp := s.responses[s.idx]
	s.idx++
	return resp, nil
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestOrchestratorEndToEnd -v`
Expected: FAIL — Orchestrator undefined

**Step 3: Implement Orchestrator**

Create `internal/evolution/orchestrator.go`:

```go
package evolution

import "context"

type OrchestratorConfig struct {
	ProjectDir    string
	WorkflowName  string
	LLM           LLMClient
	MinConfidence string
}

type Orchestrator struct {
	cfg        OrchestratorConfig
	collector  *Collector
	classifier *Classifier
	reflector  *Reflector
	curator    *Curator
	scriptGen  *ScriptGenerator
	mutator    // uses dsl.Mutator
	audit      *AuditLogger
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator { ... }
```

The `Run(ctx, triggerRunID, runStore, captureStore)` method:
1. Collector gathers data
2. Classifier categorizes the triggering run
3. Reflector extracts lessons
4. For each lesson, routes to the appropriate branch:
   - `prompt_improvement` → Curator
   - `new_step` → ScriptGenerator + DSL Mutator
5. AuditLogger records everything
6. Returns `EvolutionResult`

**Step 4: Run tests**

Run: `go test ./internal/evolution/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add Orchestrator wiring all pipeline stages"
```

---

## Task 17: LLM Client Implementation

**Files:**
- Create: `internal/evolution/llmclient.go`
- Create: `internal/evolution/llmclient_test.go`

The evolution system needs to invoke an LLM. Reuse the same pattern as the prompt
adapter — shell out to a command (e.g., `claude -p`).

**Step 1: Write failing test**

```go
func TestCommandLLMClient(t *testing.T) {
	// Use echo as a fake LLM
	c := &CommandLLMClient{Command: "cat", Args: []string{}}
	result, err := c.Complete(context.Background(), "system", "user prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "user prompt")
}
```

**Step 2: Run test, implement, run test again**

The `CommandLLMClient` takes a system prompt and user prompt, combines them, and
pipes to the command via stdin. Captures stdout as the response.

**Step 3: Commit**

```bash
git add internal/evolution/
git commit -m "feat(evolution): add CommandLLMClient for LLM invocation"
```

---

## Task 18: Daemon Integration — Trigger and Debounce

**Files:**
- Modify: `internal/adapters/grpc/server.go`
- Modify: `cmd/cloched/main.go`
- Create: `internal/evolution/trigger.go`
- Create: `internal/evolution/trigger_test.go`

**Step 1: Write failing test for trigger debounce**

Create `internal/evolution/trigger_test.go`:

```go
package evolution

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTriggerDebounce(t *testing.T) {
	var count atomic.Int32
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 1,
		RunFunc: func(projectDir, workflowName, runID string) {
			count.Add(1)
		},
	})
	defer trigger.Stop()

	// Fire 3 events rapidly for the same project+workflow
	trigger.Fire("/project", "develop", "run-1")
	trigger.Fire("/project", "develop", "run-2")
	trigger.Fire("/project", "develop", "run-3")

	// Wait for debounce to fire
	time.Sleep(2 * time.Second)

	// Should have fired only once (debounced)
	assert.Equal(t, int32(1), count.Load())
}

func TestTriggerDifferentProjectsRunIndependently(t *testing.T) {
	var count atomic.Int32
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 1,
		RunFunc: func(projectDir, workflowName, runID string) {
			count.Add(1)
		},
	})
	defer trigger.Stop()

	trigger.Fire("/project-a", "develop", "run-1")
	trigger.Fire("/project-b", "develop", "run-2")

	time.Sleep(2 * time.Second)

	// Two different projects = two independent triggers
	assert.Equal(t, int32(2), count.Load())
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/evolution/ -run TestTrigger -v`
Expected: FAIL — Trigger undefined

**Step 3: Implement Trigger**

Create `internal/evolution/trigger.go`:

The Trigger:
1. Maintains a map of `projectDir+workflowName` → debounce timer
2. On `Fire()`, resets the timer for that key (debounce)
3. When the timer fires, calls `RunFunc` with the latest runID
4. Uses a mutex per key to prevent concurrent evolution passes
5. `Stop()` cancels all pending timers

**Step 4: Run tests**

Run: `go test ./internal/evolution/ -run TestTrigger -v`
Expected: ALL PASS

**Step 5: Wire into daemon**

In `cmd/cloched/main.go`:
1. Load project config (needs project_dir from run, so load lazily or use defaults)
2. Create evolution `Trigger` with debounce from config
3. Pass trigger to `ClocheServer`

In `internal/adapters/grpc/server.go`:
1. Add `evolution *evolution.Trigger` field to `ClocheServer`
2. In `trackRun()`, after the run completes (around line 178), call:
```go
if s.evolution != nil {
    s.evolution.Fire(run.ProjectDir, run.WorkflowName, runID)
}
```

**Step 6: Run all server tests**

Run: `go test ./internal/adapters/grpc/ -v`
Expected: ALL PASS (trigger is nil in existing tests, so no evolution fires)

**Step 7: Commit**

```bash
git add internal/evolution/ internal/adapters/grpc/ cmd/cloched/
git commit -m "feat(daemon): integrate evolution trigger with debounce"
```

---

## Task 19: Integration Test

**Files:**
- Create: `test/integration/evolution_test.go`

**Step 1: Write integration test**

This test exercises the full pipeline without a real LLM — uses a fake LLM
that returns scripted responses.

```go
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvolutionPipelineIntegration(t *testing.T) {
	dir := t.TempDir()

	// Set up a realistic project structure
	setupTestProject(t, dir)

	// Create an orchestrator with a scripted LLM
	llm := newScriptedLLM(
		`{"classification": "bug"}`,
		`{"lessons": [{"id": "L001", "category": "prompt_improvement", "target": "prompts/implement.md", "insight": "Test insight", "suggested_action": "Add a rule", "evidence": ["run-1"], "confidence": "high"}]}`,
		"Updated prompt content with new rule.\n",
	)

	orch := evolution.NewOrchestrator(evolution.OrchestratorConfig{
		ProjectDir:    dir,
		WorkflowName:  "develop",
		LLM:           llm,
		MinConfidence: "medium",
	})

	result, err := orch.Run(context.Background(), "run-1", nil, nil)
	require.NoError(t, err)

	// Verify changes were made
	assert.Len(t, result.Changes, 1)

	// Verify audit trail
	logPath := filepath.Join(dir, ".cloche", "evolution", "log.jsonl")
	logContent, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logContent), "L001")

	// Verify knowledge base was updated
	kbPath := filepath.Join(dir, ".cloche", "evolution", "knowledge", "develop.md")
	kbContent, err := os.ReadFile(kbPath)
	require.NoError(t, err)
	assert.Contains(t, string(kbContent), "L001")

	// Verify snapshot was created
	snapDir := filepath.Join(dir, ".cloche", "evolution", "snapshots")
	entries, _ := os.ReadDir(snapDir)
	assert.NotEmpty(t, entries)
}

func setupTestProject(t *testing.T, dir string) {
	t.Helper()
	dirs := []string{
		".cloche/evolution/knowledge",
		".cloche/evolution/snapshots",
		"prompts",
	}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(dir, d), 0755)
	}

	files := map[string]string{
		".cloche/evolution/knowledge/develop.md": "# Knowledge Base: develop\n",
		"prompts/implement.md":                    "Write good code.\n",
		"develop.cloche": `workflow develop {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  implement:success -> test
  test:success -> done
  test:fail -> abort
}`,
	}
	for path, content := range files {
		os.WriteFile(filepath.Join(dir, path), []byte(content), 0644)
	}
}
```

**Step 2: Run integration test**

Run: `go test ./test/integration/ -run TestEvolutionPipelineIntegration -v`
Expected: PASS

**Step 3: Commit**

```bash
git add test/integration/
git commit -m "test: add evolution pipeline integration test"
```

---

## Task 20: Final Verification

**Step 1: Run all tests**

```bash
go test ./... -v
```
Expected: ALL PASS

**Step 2: Run linter (if configured)**

```bash
go vet ./...
```
Expected: No issues

**Step 3: Verify the full project compiles**

```bash
go build ./cmd/cloche && go build ./cmd/cloched && go build ./cmd/cloche-agent
```
Expected: All three binaries build successfully
