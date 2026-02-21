# Edge Selection, Fanout, and Collect — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the exit-code-only result mechanism with a stdout-marker protocol, add fanout (parallel branches) and collect (join) to the workflow engine.

**Architecture:** Bottom-up: domain types → result extraction → DSL parsing → validation → adapters → engine rewrite → runner/example updates. Each layer is testable in isolation before the next builds on it.

**Tech Stack:** Go, testify, `bufio` for stdout scanning.

**Design doc:** `docs/plans/2026-02-20-edge-selection-design.md`

---

### Task 1: Add domain types for Collect

**Files:**
- Modify: `internal/domain/workflow.go`

**Step 1: Add Collect types after the existing Wire type**

After the `Wire` struct (line 27), add:

```go
type CollectMode string

const (
	CollectAll CollectMode = "all"
	CollectAny CollectMode = "any"
)

type WireCondition struct {
	Step   string
	Result string
}

type Collect struct {
	Mode       CollectMode
	Conditions []WireCondition
	To         string
}
```

**Step 2: Add `Collects` field to `Workflow` struct**

Add `Collects []Collect` after the `Wiring` field in the `Workflow` struct.

**Step 3: Verify it compiles**

Run: `go build ./internal/domain/...`
Expected: success, no errors.

**Step 4: Commit**

```bash
git add internal/domain/workflow.go
git commit -m "feat(domain): add Collect, CollectMode, WireCondition types"
```

---

### Task 2: Replace NextStep with NextSteps

**Files:**
- Modify: `internal/domain/workflow.go`
- Modify: `internal/domain/workflow_test.go`

**Step 1: Write failing tests for NextSteps**

Replace the existing `TestWorkflow_NextStep` test and add a fanout test in `workflow_test.go`:

```go
func TestWorkflow_NextSteps(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"check": {Name: "check", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "check", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	next, err := wf.NextSteps("code", "success")
	require.NoError(t, err)
	assert.Equal(t, []string{"check"}, next)

	next, err = wf.NextSteps("check", "pass")
	require.NoError(t, err)
	assert.Equal(t, []string{domain.StepDone}, next)

	next, err = wf.NextSteps("code", "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, next)
}

func TestWorkflow_NextSteps_Fanout(t *testing.T) {
	wf := &domain.Workflow{
		Name: "fanout",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test": {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
			"lint": {Name: "lint", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "pass", To: domain.StepDone},
			{From: "lint", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	next, err := wf.NextSteps("code", "success")
	require.NoError(t, err)
	assert.Equal(t, []string{"test", "lint"}, next)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/ -run TestWorkflow_NextSteps -v`
Expected: FAIL — `NextSteps` not defined.

**Step 3: Implement NextSteps, keep NextStep as deprecated wrapper**

In `workflow.go`, add `NextSteps` and update `NextStep`:

```go
func (w *Workflow) NextSteps(stepName, result string) ([]string, error) {
	var targets []string
	for _, wire := range w.Wiring {
		if wire.From == stepName && wire.Result == result {
			targets = append(targets, wire.To)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("workflow %q: no wiring for step %q result %q", w.Name, stepName, result)
	}
	return targets, nil
}

func (w *Workflow) NextStep(stepName, result string) (string, error) {
	targets, err := w.NextSteps(stepName, result)
	if err != nil {
		return "", err
	}
	return targets[0], nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/ -v`
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/domain/workflow.go internal/domain/workflow_test.go
git commit -m "feat(domain): add NextSteps for fanout, keep NextStep as wrapper"
```

---

### Task 3: Add result extraction protocol

**Files:**
- Create: `internal/protocol/result.go`
- Create: `internal/protocol/result_test.go`

**Step 1: Write failing tests**

Create `internal/protocol/result_test.go`:

```go
package protocol_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestExtractResult_Found(t *testing.T) {
	output := []byte("some output\nCLOCHE_RESULT:needs_research\nmore output\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "needs_research", result)
	assert.NotContains(t, string(clean), "CLOCHE_RESULT")
	assert.Contains(t, string(clean), "some output")
	assert.Contains(t, string(clean), "more output")
}

func TestExtractResult_LastWins(t *testing.T) {
	output := []byte("CLOCHE_RESULT:first\nstuff\nCLOCHE_RESULT:second\n")
	result, _, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "second", result)
}

func TestExtractResult_NotFound(t *testing.T) {
	output := []byte("just normal output\nexit 0\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.False(t, found)
	assert.Empty(t, result)
	assert.Equal(t, output, clean)
}

func TestExtractResult_EmptyOutput(t *testing.T) {
	result, clean, found := protocol.ExtractResult([]byte{})
	assert.False(t, found)
	assert.Empty(t, result)
	assert.Empty(t, clean)
}

func TestExtractResult_MarkerOnly(t *testing.T) {
	output := []byte("CLOCHE_RESULT:success\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "success", result)
	assert.Empty(t, string(clean))
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/protocol/ -run TestExtractResult -v`
Expected: FAIL — `ExtractResult` not defined.

**Step 3: Implement ExtractResult**

Create `internal/protocol/result.go`:

```go
package protocol

import (
	"bytes"
	"strings"
)

const ResultPrefix = "CLOCHE_RESULT:"

// ExtractResult scans output for the last CLOCHE_RESULT:<name> line.
// Returns the result name, the output with all marker lines removed, and
// whether a marker was found.
func ExtractResult(output []byte) (result string, cleanOutput []byte, found bool) {
	var clean [][]byte
	for _, line := range bytes.Split(output, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, ResultPrefix) {
			result = trimmed[len(ResultPrefix):]
			found = true
		} else {
			clean = append(clean, line)
		}
	}
	// Rejoin and trim trailing empty line from split
	joined := bytes.Join(clean, []byte("\n"))
	joined = bytes.TrimRight(joined, "\n")
	if len(joined) > 0 {
		joined = append(joined, '\n')
	}
	return result, joined, found
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/protocol/ -run TestExtractResult -v`
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/protocol/result.go internal/protocol/result_test.go
git commit -m "feat(protocol): add ExtractResult for CLOCHE_RESULT stdout marker"
```

---

### Task 4: Update generic adapter to use result extraction

**Files:**
- Modify: `internal/adapters/agents/generic/generic.go`
- Modify: `internal/adapters/agents/generic/generic_test.go`

**Step 1: Write failing tests for marker support**

Add to `generic_test.go`:

```go
func TestGenericAdapter_StdoutMarkerOverridesExitCode(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"run": "echo 'analyzing...' && echo 'CLOCHE_RESULT:needs_research'"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", result)

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

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "bug_fix", result)
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/agents/generic/ -run TestGenericAdapter_StdoutMarker -v`
Expected: FAIL — returns "success" or "fail" instead of the marker value.

**Step 3: Update generic adapter**

Replace the `Execute` method in `generic.go`. Remove `resultOrDefault`. Use `protocol.ExtractResult`:

```go
package generic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
)

type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return "generic"
}

func (a *Adapter) Execute(ctx context.Context, step *domain.Step, workDir string) (string, error) {
	cmdStr := step.Config["run"]
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()

	// Extract result marker before writing logs
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write cleaned output to log file
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, step.Name+".log"), cleanOutput, 0644)
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			if found {
				return markerResult, nil
			}
			return "fail", nil
		}
		return "", err
	}

	if found {
		return markerResult, nil
	}
	return "success", nil
}
```

**Step 4: Run all generic adapter tests**

Run: `go test ./internal/adapters/agents/generic/ -v`
Expected: all PASS. Existing tests may need result name updates (`"pass"` → `"success"`) since `resultOrDefault` is gone. Update the `TestGenericAdapter_CapturesOutput` and `TestGenericAdapter_CapturesOutputOnFailure` tests: change their `Results` to `[]string{"success", "fail"}` and expected results accordingly. The `TestGenericAdapter_CapturesOutput` test currently expects `"pass"` for exit 0 — it should now expect `"success"` (update the `Results` field from `["pass", "fail"]` to `["success", "fail"]`).

**Step 5: Commit**

```bash
git add internal/adapters/agents/generic/generic.go internal/adapters/agents/generic/generic_test.go
git commit -m "feat(generic): use ExtractResult protocol, remove resultOrDefault"
```

---

### Task 5: Update prompt adapter to use result extraction

**Files:**
- Modify: `internal/adapters/agents/prompt/prompt.go`
- Modify: `internal/adapters/agents/prompt/prompt_test.go`

**Step 1: Write failing test for result instruction injection**

Add to `prompt_test.go`:

```go
func TestPromptAdapter_InjectsResultInstructions(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "cat > captured_prompt.txt"},
	}

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"prompt": "Analyze the code."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "CLOCHE_RESULT:success")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:fail")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:needs_research")
}

func TestPromptAdapter_StdoutMarkerSelectsResult(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "cat > /dev/null && echo 'CLOCHE_RESULT:needs_research'"},
	}

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"prompt": "Analyze the code."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", result)
}
```

**Step 2: Run to verify they fail**

Run: `go test ./internal/adapters/agents/prompt/ -run "TestPromptAdapter_Injects|TestPromptAdapter_StdoutMarker" -v`
Expected: FAIL.

**Step 3: Update prompt adapter**

Key changes to `prompt.go`:
1. `assemblePrompt` takes `step.Results` and appends result selection instructions.
2. After the command runs, capture stdout and use `protocol.ExtractResult`.
3. Remove `resultOrDefault`.
4. Fall back to exit-code convention if no marker found.

The `Execute` method needs to capture stdout (currently it uses `cmd.Stdout = &stdout` but doesn't scan for markers). Add extraction after `cmd.Run()`.

In `assemblePrompt`, append a new section:

```go
// 4. Result selection instructions
if len(step.Results) > 0 {
	var resultLines []string
	resultLines = append(resultLines, "## Result Selection")
	resultLines = append(resultLines, "When you are finished, output exactly one of the following on its own line:")
	for _, r := range step.Results {
		resultLines = append(resultLines, protocol.ResultPrefix+r)
	}
	parts = append(parts, strings.Join(resultLines, "\n"))
}
```

In `Execute`, after `cmd.Run()`:

```go
markerResult, _, found := protocol.ExtractResult(stdout.Bytes())

if err := cmd.Run(); err != nil {
	if _, ok := err.(*exec.ExitError); ok {
		if found {
			return markerResult, nil
		}
		return "fail", nil
	}
	return "", err
}

if found {
	return markerResult, nil
}
return "success", nil
```

**Step 4: Run all prompt adapter tests**

Run: `go test ./internal/adapters/agents/prompt/ -v`
Expected: all PASS. Existing tests that expect `"fixed"` as first result via `resultOrDefault` need updating — `TestPromptAdapter_IncludesFeedback` expects `"fixed"` but will now get `"success"` (exit 0, no marker). Update that test's expected result to `"success"`, or have its mock emit the marker.

**Step 5: Commit**

```bash
git add internal/adapters/agents/prompt/prompt.go internal/adapters/agents/prompt/prompt_test.go
git commit -m "feat(prompt): inject result instructions, use ExtractResult protocol"
```

---

### Task 6: Parse `collect` in DSL

**Files:**
- Modify: `internal/dsl/parser.go`
- Modify: `internal/dsl/parser_test.go`

**Step 1: Write failing test**

Add to `parser_test.go`:

```go
func TestParser_CollectAll(t *testing.T) {
	input := `workflow "parallel" {
  step code {
    prompt = "write code"
    results = [success]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  step lint {
    run = "make lint"
    results = [success, fail]
  }
  step merge {
    run = "echo merged"
    results = [success]
  }

  code:success -> test
  code:success -> lint
  test:fail -> abort
  lint:fail -> abort
  collect all(test:success, lint:success) -> merge
  merge:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Collects, 1)
	c := wf.Collects[0]
	assert.Equal(t, domain.CollectAll, c.Mode)
	assert.Equal(t, "merge", c.To)
	require.Len(t, c.Conditions, 2)
	assert.Equal(t, "test", c.Conditions[0].Step)
	assert.Equal(t, "success", c.Conditions[0].Result)
	assert.Equal(t, "lint", c.Conditions[1].Step)
	assert.Equal(t, "success", c.Conditions[1].Result)
}

func TestParser_CollectAny(t *testing.T) {
	input := `workflow "race" {
  step fast {
    run = "echo fast"
    results = [success]
  }
  step slow {
    run = "sleep 1 && echo slow"
    results = [success]
  }
  step next {
    run = "echo next"
    results = [success]
  }

  collect any(fast:success, slow:success) -> next
  next:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Collects, 1)
	assert.Equal(t, domain.CollectAny, wf.Collects[0].Mode)
}
```

**Step 2: Run to verify they fail**

Run: `go test ./internal/dsl/ -run "TestParser_Collect" -v`
Expected: FAIL — parser doesn't recognize `collect`.

**Step 3: Implement collect parsing**

In `parser.go`, update the `parseWorkflow` loop. When `p.current` is `TokenIdent` with literal `"collect"`, call a new `parseCollect` method:

```go
// In the parseWorkflow loop, add a branch before the wire check:
} else if p.current.Type == TokenIdent && p.current.Literal == "collect" {
	collect, err := p.parseCollect()
	if err != nil {
		return nil, err
	}
	wf.Collects = append(wf.Collects, collect)
}
```

Add the `parseCollect` method:

```go
func (p *Parser) parseCollect() (domain.Collect, error) {
	p.advance() // consume "collect"

	modeTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Collect{}, fmt.Errorf("expected 'all' or 'any': %w", err)
	}

	var mode domain.CollectMode
	switch modeTok.Literal {
	case "all":
		mode = domain.CollectAll
	case "any":
		mode = domain.CollectAny
	default:
		return domain.Collect{}, fmt.Errorf("line %d col %d: expected 'all' or 'any', got %q",
			modeTok.Line, modeTok.Col, modeTok.Literal)
	}

	if _, err := p.expect(TokenLParen); err != nil {
		return domain.Collect{}, err
	}

	var conditions []domain.WireCondition
	for p.current.Type != TokenRParen && p.current.Type != TokenEOF {
		stepTok, err := p.expect(TokenIdent)
		if err != nil {
			return domain.Collect{}, fmt.Errorf("expected step name in collect: %w", err)
		}
		if _, err := p.expect(TokenColon); err != nil {
			return domain.Collect{}, err
		}
		resultTok, err := p.expect(TokenIdent)
		if err != nil {
			return domain.Collect{}, fmt.Errorf("expected result name in collect: %w", err)
		}
		conditions = append(conditions, domain.WireCondition{Step: stepTok.Literal, Result: resultTok.Literal})
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRParen); err != nil {
		return domain.Collect{}, err
	}
	if _, err := p.expect(TokenArrow); err != nil {
		return domain.Collect{}, err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Collect{}, fmt.Errorf("expected target step: %w", err)
	}

	return domain.Collect{
		Mode:       mode,
		Conditions: conditions,
		To:         toTok.Literal,
	}, nil
}
```

**Step 4: Run tests**

Run: `go test ./internal/dsl/ -v`
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/dsl/parser.go internal/dsl/parser_test.go
git commit -m "feat(dsl): parse collect all/any wiring syntax"
```

---

### Task 7: Update validation for collects

**Files:**
- Modify: `internal/domain/workflow.go`
- Modify: `internal/domain/workflow_test.go`

**Step 1: Write failing tests**

Add to `workflow_test.go`:

```go
func TestWorkflow_Validate_CollectValid(t *testing.T) {
	wf := &domain.Workflow{
		Name: "parallel",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"lint":  {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"merge": {Name: "merge", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "fail", To: domain.StepAbort},
			{From: "lint", Result: "fail", To: domain.StepAbort},
			{From: "merge", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAll,
				Conditions: []domain.WireCondition{
					{Step: "test", Result: "success"},
					{Step: "lint", Result: "success"},
				},
				To: "merge",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	assert.NoError(t, err)
}

func TestWorkflow_Validate_CollectBadStep(t *testing.T) {
	wf := &domain.Workflow{
		Name: "bad-collect",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode:       domain.CollectAll,
				Conditions: []domain.WireCondition{{Step: "nonexistent", Result: "success"}},
				To:         "code",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestWorkflow_Validate_CollectBadTarget(t *testing.T) {
	wf := &domain.Workflow{
		Name: "bad-target",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode:       domain.CollectAll,
				Conditions: []domain.WireCondition{{Step: "code", Result: "success"}},
				To:         "nonexistent",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}
```

**Step 2: Run to verify they fail**

Run: `go test ./internal/domain/ -run "TestWorkflow_Validate_Collect" -v`
Expected: `TestWorkflow_Validate_CollectValid` may pass (no validation yet = no rejection), but `CollectBadStep` and `CollectBadTarget` will fail (no error returned).

**Step 3: Update Validate method**

In the `Validate()` method in `workflow.go`, after the existing wired-results check, add:

1. Count results covered by collects (so they satisfy the "every result must be wired" check).
2. Validate collect conditions reference real steps and declared results.
3. Validate collect targets exist.

```go
// After building the wired map, also count results covered by collects
for _, c := range w.Collects {
	// Validate target step exists
	if c.To != StepDone && c.To != StepAbort {
		if _, ok := w.Steps[c.To]; !ok {
			return fmt.Errorf("workflow %q: collect target step %q not found", w.Name, c.To)
		}
		reachable[c.To] = true
	}

	for _, cond := range c.Conditions {
		// Validate condition step exists
		step, ok := w.Steps[cond.Step]
		if !ok {
			return fmt.Errorf("workflow %q: collect references unknown step %q", w.Name, cond.Step)
		}
		// Validate condition result is declared
		found := false
		for _, r := range step.Results {
			if r == cond.Result {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("workflow %q: collect references undeclared result %q on step %q", w.Name, cond.Result, cond.Step)
		}
		// Mark as wired
		if wired[cond.Step] == nil {
			wired[cond.Step] = make(map[string]bool)
		}
		wired[cond.Step][cond.Result] = true
	}
}
```

**Step 4: Run all domain tests**

Run: `go test ./internal/domain/ -v`
Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/domain/workflow.go internal/domain/workflow_test.go
git commit -m "feat(domain): validate collect conditions and targets"
```

---

### Task 8: Rewrite engine as DAG walker

**Files:**
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`

This is the largest task. The engine changes from a single-step loop to a concurrent DAG walker.

**Step 1: Write failing tests for fanout and collect**

Add to `engine_test.go`:

```go
func TestEngine_Fanout(t *testing.T) {
	wf := &domain.Workflow{
		Name: "fanout",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test": {Name: "test", Type: domain.StepTypeScript, Results: []string{"success"}},
			"lint": {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "success", To: domain.StepDone},
			{From: "lint", Result: "success", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "success", "lint": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Contains(t, exec.called, "code")
	assert.Contains(t, exec.called, "test")
	assert.Contains(t, exec.called, "lint")
}

func TestEngine_CollectAll(t *testing.T) {
	wf := &domain.Workflow{
		Name: "collect-all",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"success"}},
			"lint":  {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success"}},
			"merge": {Name: "merge", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "merge", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAll,
				Conditions: []domain.WireCondition{
					{Step: "test", Result: "success"},
					{Step: "lint", Result: "success"},
				},
				To: "merge",
			},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "success", "lint": "success", "merge": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Contains(t, exec.called, "merge")
}

func TestEngine_CollectAny(t *testing.T) {
	wf := &domain.Workflow{
		Name: "collect-any",
		Steps: map[string]*domain.Step{
			"fast":  {Name: "fast", Type: domain.StepTypeScript, Results: []string{"success"}},
			"slow":  {Name: "slow", Type: domain.StepTypeScript, Results: []string{"success"}},
			"next":  {Name: "next", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "fast", Result: "success", To: domain.StepDone},
			{From: "slow", Result: "success", To: domain.StepDone},
			{From: "next", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAny,
				Conditions: []domain.WireCondition{
					{Step: "fast", Result: "success"},
					{Step: "slow", Result: "success"},
				},
				To: "next",
			},
		},
		EntryStep: "fast",
	}

	// fast and slow both started somehow (need fanout to entry for this to work)
	// For simplicity, test with sequential: fast completes, any-collect fires
	exec := &fakeExecutor{results: map[string]string{
		"fast": "success", "slow": "success", "next": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Contains(t, exec.called, "next")
}

func TestEngine_UndeclaredResultAborts(t *testing.T) {
	wf := &domain.Workflow{
		Name: "undeclared",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "code", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "code",
	}

	// Return a result not in declared list
	exec := &fakeExecutor{results: map[string]string{"code": "unknown"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.Error(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Contains(t, err.Error(), "undeclared")
}
```

Note: the `fakeExecutor` needs to be made thread-safe for concurrent access. Update it:

```go
type fakeExecutor struct {
	mu      sync.Mutex
	results map[string]string
	called  []string
}

func (f *fakeExecutor) Execute(_ context.Context, step *domain.Step) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, step.Name)
	return f.results[step.Name], nil
}
```

**Step 2: Run to verify they fail**

Run: `go test ./internal/engine/ -run "TestEngine_Fanout|TestEngine_Collect|TestEngine_Undeclared" -v`
Expected: FAIL — engine doesn't support fanout/collect.

**Step 3: Rewrite engine**

Replace the `Run` method in `engine.go` with the DAG walker. Key changes:

1. Track `active` step count, `completed` map, and `pending` collect states.
2. Use a results channel for step completion notifications.
3. When a step completes: validate result is declared, look up simple wires via `NextSteps`, check collect conditions, launch new steps.
4. Terminate when active count hits 0.
5. Track terminal states — all `done` = succeeded, any `abort` = failed.

The full implementation involves:
- A `stepResult` struct sent on the channel: `{stepName, result, err}`.
- A `launchStep` helper that increments active count and starts a goroutine.
- The main loop: receive from channel, process result, launch new steps.
- Collect state tracking: for each `Collect`, a set of satisfied condition indices.

The existing `StatusHandler` interface stays the same — `OnStepStart`/`OnStepComplete` are called per step as before.

**Step 4: Run all engine tests**

Run: `go test ./internal/engine/ -v`
Expected: all PASS (both old and new tests).

**Step 5: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go
git commit -m "feat(engine): rewrite as concurrent DAG walker with fanout/collect"
```

---

### Task 9: Update Run model for concurrent steps

**Files:**
- Modify: `internal/domain/run.go`

**Step 1: Replace `CurrentStep` with `ActiveSteps`**

Change the `Run` struct:

```go
type Run struct {
	ID             string
	WorkflowName   string
	State          RunState
	ActiveSteps    []string  // replaces CurrentStep
	StepExecutions []*StepExecution
	StartedAt      time.Time
	CompletedAt    time.Time
}
```

Update `RecordStepStart` to append to `ActiveSteps` instead of overwriting `CurrentStep`. Update `RecordStepComplete` to remove from `ActiveSteps`.

**Step 2: Verify it compiles and tests pass**

Run: `go test ./... 2>&1 | tail -20`

Fix any compilation errors from callers that reference `CurrentStep` (likely the engine and runner). Update them to use `ActiveSteps`.

**Step 3: Commit**

```bash
git add internal/domain/run.go
git commit -m "feat(domain): replace CurrentStep with ActiveSteps for concurrency"
```

---

### Task 10: Update runner and status reporting

**Files:**
- Modify: `internal/agent/runner.go`

**Step 1: Verify runner compiles with new engine API**

The runner creates the engine and calls `eng.Run()`. The engine's API (`Run(ctx, wf)`) hasn't changed — the concurrency is internal. The `stepExecutor` and `statusReporter` interfaces are unchanged.

Run: `go build ./internal/agent/...`
Expected: success. If `CurrentStep` was referenced, update to `ActiveSteps`.

**Step 2: Run full test suite**

Run: `go test ./... -v 2>&1 | tail -40`
Expected: all PASS.

**Step 3: Commit if any changes were needed**

```bash
git add internal/agent/runner.go
git commit -m "fix(runner): adapt to concurrent engine and ActiveSteps"
```

---

### Task 11: Update example workflows

**Files:**
- Modify: `examples/ruby-calculator/develop.cloche`
- Modify: `examples/hello-world/build.cloche` (if it uses `pass`)
- Modify: `examples/hello-world/flaky.cloche` (if it uses `pass`)

**Step 1: Update ruby-calculator**

Change `results = [pass, fail]` to `results = [success, fail]` in `test` and `lint` steps. Update wiring from `test:pass` → `test:success` and `lint:pass` → `lint:success`. Change `fix` step results from `[fixed, give-up]` to `[success, fail, give-up]` (or keep custom names — the prompt adapter will emit markers). Actually, keep `[fixed, give-up]` since those are custom edges that require markers.

Wait — the fix step currently returns `"fixed"` via `resultOrDefault` (exit 0, first result). With the new protocol, exit 0 = `"success"`, which isn't declared. The fix step needs updating. Options:
- Change results to `[success, fail, give-up]` and wiring to `fix:success -> test`.
- Or have the prompt inject result instructions so the LLM emits `CLOCHE_RESULT:fixed`.

The cleaner approach: rename to `[success, fail, give-up]` since `"fixed"` was just a synonym for success. Update wiring accordingly.

```
step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
}

fix:success -> test
fix:fail -> abort
fix:give-up -> abort
```

Similarly, `implement` step: change from `[success, fail]` — these already match convention.

**Step 2: Update hello-world workflows if needed**

Check `build.cloche` and `flaky.cloche` for `pass` results and update.

**Step 3: Run the DSL parser on updated files to verify**

Run: `go run ./cmd/cloche-agent/ --help` (or a quick parse test).

**Step 4: Commit**

```bash
git add examples/
git commit -m "fix(examples): rename pass->success, update fix step results"
```

---

### Task 12: Full integration test pass

**Files:**
- All test files

**Step 1: Run complete test suite**

Run: `go test ./... -v -count=1`
Expected: all PASS.

**Step 2: Run linter if available**

Run: `go vet ./...`
Expected: no issues.

**Step 3: Commit any final fixes**

---
