# Cloche MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a working Cloche MVP — parse workflow DSL, execute steps in Docker containers, report status via gRPC.

**Architecture:** Three Go binaries (cloche, cloched, cloche-agent) with hexagonal architecture. Domain types and DSL parser are the foundation. Build bottom-up: domain -> DSL -> engine -> agent binary -> daemon -> CLI.

**Tech Stack:** Go 1.22+, protobuf/gRPC, Docker SDK, SQLite (via modernc.org/sqlite — pure Go), testify for assertions.

---

## Phase 1: Foundation

### Task 1: Initialize Go Module and Directory Structure

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `cmd/cloche/main.go`
- Create: `cmd/cloched/main.go`
- Create: `cmd/cloche-agent/main.go`

**Step 1: Initialize Go module**

```bash
cd /home/lucas/workspace/cloche
go mod init github.com/cloche-dev/cloche
```

**Step 2: Create directory structure**

```bash
mkdir -p cmd/cloche cmd/cloched cmd/cloche-agent
mkdir -p internal/domain internal/ports
mkdir -p internal/adapters/docker internal/adapters/sqlite
mkdir -p internal/adapters/grpc internal/adapters/agents/generic
mkdir -p internal/adapters/agents/claudecode
mkdir -p internal/dsl internal/engine internal/protocol
```

**Step 3: Create placeholder mains**

`cmd/cloche/main.go`:
```go
package main

func main() {}
```

`cmd/cloched/main.go`:
```go
package main

func main() {}
```

`cmd/cloche-agent/main.go`:
```go
package main

func main() {}
```

**Step 4: Create Makefile**

```makefile
.PHONY: build test lint clean

build:
	go build -o bin/cloche ./cmd/cloche
	go build -o bin/cloched ./cmd/cloched
	go build -o bin/cloche-agent ./cmd/cloche-agent

test:
	go test ./... -v

test-short:
	go test ./... -short

lint:
	go vet ./...

clean:
	rm -rf bin/
```

**Step 5: Verify build**

```bash
make build
```
Expected: three binaries in `bin/`

**Step 6: Commit**

```bash
git add -A && git commit -m "feat: initialize Go module and project structure"
```

---

## Phase 2: Domain Types

### Task 2: Workflow Domain Types

**Files:**
- Create: `internal/domain/workflow.go`
- Create: `internal/domain/workflow_test.go`

**Step 1: Write tests for workflow types**

`internal/domain/workflow_test.go`:
```go
package domain_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflow_Validate_ValidGraph(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
			},
			"check": {
				Name:    "check",
				Type:    domain.StepTypeScript,
				Results: []string{"pass", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "code", Result: "fail", To: domain.StepAbort},
			{From: "check", Result: "pass", To: domain.StepDone},
			{From: "check", Result: "fail", To: "code"},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	assert.NoError(t, err)
}

func TestWorkflow_Validate_UnwiredResult(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			// "fail" is not wired
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail")
}

func TestWorkflow_Validate_OrphanStep(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
			},
			"orphan": {
				Name:    "orphan",
				Type:    domain.StepTypeScript,
				Results: []string{"done"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "orphan", Result: "done", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orphan")
}

func TestWorkflow_Validate_NoEntryStep(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "test-workflow",
		Steps:     map[string]*domain.Step{},
		Wiring:    []domain.Wire{},
		EntryStep: "",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entry")
}

func TestWorkflow_NextStep(t *testing.T) {
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

	next, err := wf.NextStep("code", "success")
	require.NoError(t, err)
	assert.Equal(t, "check", next)

	next, err = wf.NextStep("check", "pass")
	require.NoError(t, err)
	assert.Equal(t, domain.StepDone, next)

	_, err = wf.NextStep("code", "nonexistent")
	assert.Error(t, err)
}
```

**Step 2: Run tests to verify they fail**

```bash
go test ./internal/domain/ -v
```
Expected: compilation errors (types don't exist yet)

**Step 3: Implement domain types**

`internal/domain/workflow.go`:
```go
package domain

import "fmt"

const (
	StepDone  = "done"
	StepAbort = "abort"
)

type StepType string

const (
	StepTypeAgent  StepType = "agent"
	StepTypeScript StepType = "script"
)

type Step struct {
	Name    string
	Type    StepType
	Results []string
	Config  map[string]string // prompt, run, image, etc.
}

type Wire struct {
	From   string
	Result string
	To     string
}

type Workflow struct {
	Name      string
	Steps     map[string]*Step
	Wiring    []Wire
	EntryStep string
}

func (w *Workflow) Validate() error {
	if w.EntryStep == "" {
		return fmt.Errorf("workflow %q: no entry step defined", w.Name)
	}
	if _, ok := w.Steps[w.EntryStep]; !ok {
		return fmt.Errorf("workflow %q: entry step %q not found", w.Name, w.EntryStep)
	}

	// Check all results are wired
	wired := make(map[string]map[string]bool)
	reachable := map[string]bool{w.EntryStep: true}
	for _, wire := range w.Wiring {
		if wired[wire.From] == nil {
			wired[wire.From] = make(map[string]bool)
		}
		wired[wire.From][wire.Result] = true
		if wire.To != StepDone && wire.To != StepAbort {
			reachable[wire.To] = true
		}
	}

	for name, step := range w.Steps {
		for _, result := range step.Results {
			if !wired[name][result] {
				return fmt.Errorf("workflow %q: step %q result %q is not wired", w.Name, name, result)
			}
		}
	}

	// Check for orphan steps (unreachable from any wiring)
	for name := range w.Steps {
		if !reachable[name] {
			return fmt.Errorf("workflow %q: step %q is orphaned (unreachable)", w.Name, name)
		}
	}

	return nil
}

func (w *Workflow) NextStep(stepName, result string) (string, error) {
	for _, wire := range w.Wiring {
		if wire.From == stepName && wire.Result == result {
			return wire.To, nil
		}
	}
	return "", fmt.Errorf("workflow %q: no wiring for step %q result %q", w.Name, stepName, result)
}
```

**Step 4: Install testify and run tests**

```bash
go get github.com/stretchr/testify
go test ./internal/domain/ -v
```
Expected: all tests PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add workflow domain types with validation"
```

### Task 3: Run and Capture Domain Types

**Files:**
- Create: `internal/domain/run.go`
- Create: `internal/domain/run_test.go`

**Step 1: Write tests**

`internal/domain/run_test.go`:
```go
package domain_test

import (
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestRun_Lifecycle(t *testing.T) {
	run := domain.NewRun("run-1", "test-workflow")
	assert.Equal(t, domain.RunStatePending, run.State)

	run.Start()
	assert.Equal(t, domain.RunStateRunning, run.State)
	assert.False(t, run.StartedAt.IsZero())

	run.RecordStepStart("code")
	assert.Equal(t, "code", run.CurrentStep)
	assert.Len(t, run.StepExecutions, 1)

	run.RecordStepComplete("code", "success")
	assert.Equal(t, "success", run.StepExecutions[0].Result)
	assert.False(t, run.StepExecutions[0].CompletedAt.IsZero())

	run.Complete(domain.RunStateSucceeded)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.False(t, run.CompletedAt.IsZero())
}

func TestRun_StepExecution_Duration(t *testing.T) {
	exec := &domain.StepExecution{
		StepName:    "code",
		StartedAt:   time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
	}
	assert.InDelta(t, 5.0, exec.Duration().Seconds(), 0.1)
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/domain/ -v -run TestRun
```

**Step 3: Implement**

`internal/domain/run.go`:
```go
package domain

import "time"

type RunState string

const (
	RunStatePending   RunState = "pending"
	RunStateRunning   RunState = "running"
	RunStateSucceeded RunState = "succeeded"
	RunStateFailed    RunState = "failed"
	RunStateCancelled RunState = "cancelled"
)

type StepExecution struct {
	StepName    string
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
	Logs        string
	GitRef      string // output state
}

func (e *StepExecution) Duration() time.Duration {
	return e.CompletedAt.Sub(e.StartedAt)
}

type Run struct {
	ID             string
	WorkflowName   string
	State          RunState
	CurrentStep    string
	StepExecutions []*StepExecution
	StartedAt      time.Time
	CompletedAt    time.Time
}

func NewRun(id, workflowName string) *Run {
	return &Run{
		ID:           id,
		WorkflowName: workflowName,
		State:        RunStatePending,
	}
}

func (r *Run) Start() {
	r.State = RunStateRunning
	r.StartedAt = time.Now()
}

func (r *Run) RecordStepStart(stepName string) {
	r.CurrentStep = stepName
	r.StepExecutions = append(r.StepExecutions, &StepExecution{
		StepName:  stepName,
		StartedAt: time.Now(),
	})
}

func (r *Run) RecordStepComplete(stepName, result string) {
	for i := len(r.StepExecutions) - 1; i >= 0; i-- {
		if r.StepExecutions[i].StepName == stepName && r.StepExecutions[i].CompletedAt.IsZero() {
			r.StepExecutions[i].Result = result
			r.StepExecutions[i].CompletedAt = time.Now()
			return
		}
	}
}

func (r *Run) Complete(state RunState) {
	r.State = state
	r.CompletedAt = time.Now()
}
```

**Step 4: Run tests**

```bash
go test ./internal/domain/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add run and step execution domain types"
```

---

## Phase 3: Workflow DSL Parser

### Task 4: DSL Lexer

**Files:**
- Create: `internal/dsl/lexer.go`
- Create: `internal/dsl/lexer_test.go`
- Create: `internal/dsl/token.go`

**Step 1: Define token types**

`internal/dsl/token.go`:
```go
package dsl

type TokenType int

const (
	// Literals
	TokenIdent  TokenType = iota // identifiers: workflow, step, etc.
	TokenString                  // "quoted string"

	// Delimiters
	TokenLBrace    // {
	TokenRBrace    // }
	TokenLBracket  // [
	TokenRBracket  // ]
	TokenLParen    // (
	TokenRParen    // )
	TokenComma     // ,
	TokenEquals    // =
	TokenArrow     // ->
	TokenColon     // :
	TokenDot       // .

	// Special
	TokenEOF
	TokenIllegal
)

type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Col     int
}
```

**Step 2: Write lexer tests**

`internal/dsl/lexer_test.go`:
```go
package dsl_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLexer_SimpleWorkflow(t *testing.T) {
	input := `workflow "test" {
  step code(agent) {
    results = [success, fail]
  }
  code:success -> done
}`

	lexer := dsl.NewLexer(input)
	tokens := lexAll(lexer)

	expected := []dsl.TokenType{
		dsl.TokenIdent,    // workflow
		dsl.TokenString,   // "test"
		dsl.TokenLBrace,   // {
		dsl.TokenIdent,    // step
		dsl.TokenIdent,    // code
		dsl.TokenLParen,   // (
		dsl.TokenIdent,    // agent
		dsl.TokenRParen,   // )
		dsl.TokenLBrace,   // {
		dsl.TokenIdent,    // results
		dsl.TokenEquals,   // =
		dsl.TokenLBracket, // [
		dsl.TokenIdent,    // success
		dsl.TokenComma,    // ,
		dsl.TokenIdent,    // fail
		dsl.TokenRBracket, // ]
		dsl.TokenRBrace,   // }
		dsl.TokenIdent,    // code
		dsl.TokenColon,    // :
		dsl.TokenIdent,    // success
		dsl.TokenArrow,    // ->
		dsl.TokenIdent,    // done
		dsl.TokenRBrace,   // }
		dsl.TokenEOF,
	}

	require.Len(t, tokens, len(expected))
	for i, tok := range tokens {
		assert.Equal(t, expected[i], tok.Type, "token %d: expected %d got %d (%q)", i, expected[i], tok.Type, tok.Literal)
	}
}

func TestLexer_StringLiteral(t *testing.T) {
	input := `"hello world"`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenString, tok.Type)
	assert.Equal(t, "hello world", tok.Literal)
}

func TestLexer_Comments(t *testing.T) {
	input := `// this is a comment
workflow`
	lexer := dsl.NewLexer(input)
	tok := lexer.NextToken()
	assert.Equal(t, dsl.TokenIdent, tok.Type)
	assert.Equal(t, "workflow", tok.Literal)
}

func TestLexer_Arrow(t *testing.T) {
	input := `a -> b`
	lexer := dsl.NewLexer(input)
	tokens := lexAll(lexer)
	assert.Equal(t, dsl.TokenIdent, tokens[0].Type)
	assert.Equal(t, dsl.TokenArrow, tokens[1].Type)
	assert.Equal(t, dsl.TokenIdent, tokens[2].Type)
}

func lexAll(l *dsl.Lexer) []dsl.Token {
	var tokens []dsl.Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == dsl.TokenEOF || tok.Type == dsl.TokenIllegal {
			break
		}
	}
	return tokens
}
```

**Step 3: Run tests — expect failure**

```bash
go test ./internal/dsl/ -v
```

**Step 4: Implement lexer**

`internal/dsl/lexer.go`:
```go
package dsl

type Lexer struct {
	input   []rune
	pos     int
	line    int
	col     int
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: []rune(input), pos: 0, line: 1, col: 1}
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= len(l.input) {
		return Token{Type: TokenEOF, Line: l.line, Col: l.col}
	}

	ch := l.input[l.pos]
	tok := Token{Line: l.line, Col: l.col}

	switch ch {
	case '{':
		tok.Type = TokenLBrace
		tok.Literal = "{"
		l.advance()
	case '}':
		tok.Type = TokenRBrace
		tok.Literal = "}"
		l.advance()
	case '[':
		tok.Type = TokenLBracket
		tok.Literal = "["
		l.advance()
	case ']':
		tok.Type = TokenRBracket
		tok.Literal = "]"
		l.advance()
	case '(':
		tok.Type = TokenLParen
		tok.Literal = "("
		l.advance()
	case ')':
		tok.Type = TokenRParen
		tok.Literal = ")"
		l.advance()
	case ',':
		tok.Type = TokenComma
		tok.Literal = ","
		l.advance()
	case '=':
		tok.Type = TokenEquals
		tok.Literal = "="
		l.advance()
	case ':':
		tok.Type = TokenColon
		tok.Literal = ":"
		l.advance()
	case '.':
		tok.Type = TokenDot
		tok.Literal = "."
		l.advance()
	case '-':
		if l.peek() == '>' {
			tok.Type = TokenArrow
			tok.Literal = "->"
			l.advance()
			l.advance()
		} else {
			tok.Type = TokenIllegal
			tok.Literal = string(ch)
			l.advance()
		}
	case '"':
		tok.Type = TokenString
		tok.Literal = l.readString()
	default:
		if isIdentStart(ch) {
			tok.Type = TokenIdent
			tok.Literal = l.readIdent()
		} else {
			tok.Type = TokenIllegal
			tok.Literal = string(ch)
			l.advance()
		}
	}

	return tok
}

func (l *Lexer) advance() {
	if l.pos < len(l.input) {
		if l.input[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func (l *Lexer) peek() rune {
	if l.pos+1 < len(l.input) {
		return l.input[l.pos+1]
	}
	return 0
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			l.advance()
		} else if ch == '/' && l.peek() == '/' {
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}
		} else {
			break
		}
	}
}

func (l *Lexer) readString() string {
	l.advance() // skip opening quote
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] != '"' {
		l.advance()
	}
	result := string(l.input[start:l.pos])
	if l.pos < len(l.input) {
		l.advance() // skip closing quote
	}
	return result
}

func (l *Lexer) readIdent() string {
	start := l.pos
	for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
		l.advance()
	}
	return string(l.input[start:l.pos])
}

func isIdentStart(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentPart(ch rune) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}
```

**Step 5: Run tests**

```bash
go test ./internal/dsl/ -v
```
Expected: all PASS

**Step 6: Commit**

```bash
git add -A && git commit -m "feat: add DSL lexer with token types"
```

### Task 5: DSL Parser

**Files:**
- Create: `internal/dsl/parser.go`
- Create: `internal/dsl/parser_test.go`

**Step 1: Write parser tests**

`internal/dsl/parser_test.go`:
```go
package dsl_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParser_FullWorkflow(t *testing.T) {
	input := `workflow "implement-feature" {
  step code(agent) {
    prompt = file("prompts/implement.md")
    results = [success, fail, retry_with_feedback]
  }

  step check(script) {
    run = "make test && make lint"
    results = [pass, fail]
  }

  code:success -> check
  code:fail -> abort
  code:retry_with_feedback -> code

  check:pass -> done
  check:fail -> code
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	assert.Equal(t, "implement-feature", wf.Name)
	assert.Len(t, wf.Steps, 2)

	code := wf.Steps["code"]
	require.NotNil(t, code)
	assert.Equal(t, domain.StepTypeAgent, code.Type)
	assert.Equal(t, []string{"success", "fail", "retry_with_feedback"}, code.Results)
	assert.Equal(t, `file("prompts/implement.md")`, code.Config["prompt"])

	check := wf.Steps["check"]
	require.NotNil(t, check)
	assert.Equal(t, domain.StepTypeScript, check.Type)
	assert.Equal(t, "make test && make lint", check.Config["run"])

	assert.Len(t, wf.Wiring, 5)
	assert.Equal(t, "code", wf.EntryStep)
}

func TestParser_MinimalWorkflow(t *testing.T) {
	input := `workflow "simple" {
  step build(script) {
    run = "make build"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "simple", wf.Name)
	assert.Equal(t, "build", wf.EntryStep)
}

func TestParser_SyntaxError(t *testing.T) {
	input := `workflow { }` // missing name string
	_, err := dsl.Parse(input)
	assert.Error(t, err)
}

func TestParser_ContainerBlock(t *testing.T) {
	input := `workflow "test" {
  step code(agent) {
    prompt = "do something"
    container {
      image = "cloche/agent:latest"
      network_allow = ["docs.python.org", "internal.example.com"]
    }
    results = [success]
  }
  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	code := wf.Steps["code"]
	assert.Equal(t, "cloche/agent:latest", code.Config["container.image"])
	assert.Equal(t, "docs.python.org,internal.example.com", code.Config["container.network_allow"])
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/dsl/ -v -run TestParser
```

**Step 3: Implement parser**

`internal/dsl/parser.go`:
```go
package dsl

import (
	"fmt"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
)

type Parser struct {
	lexer   *Lexer
	current Token
	peek    Token
}

func Parse(input string) (*domain.Workflow, error) {
	p := &Parser{lexer: NewLexer(input)}
	p.advance() // load current
	p.advance() // load peek
	return p.parseWorkflow()
}

func (p *Parser) advance() {
	p.current = p.peek
	p.peek = p.lexer.NextToken()
}

func (p *Parser) expect(typ TokenType) (Token, error) {
	if p.current.Type != typ {
		return p.current, fmt.Errorf("line %d col %d: expected token type %d, got %d (%q)",
			p.current.Line, p.current.Col, typ, p.current.Type, p.current.Literal)
	}
	tok := p.current
	p.advance()
	return tok, nil
}

func (p *Parser) parseWorkflow() (*domain.Workflow, error) {
	if _, err := p.expectIdent("workflow"); err != nil {
		return nil, err
	}

	nameTok, err := p.expect(TokenString)
	if err != nil {
		return nil, fmt.Errorf("expected workflow name string: %w", err)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	wf := &domain.Workflow{
		Name:  nameTok.Literal,
		Steps: make(map[string]*domain.Step),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if p.current.Type == TokenIdent && p.current.Literal == "step" {
			step, err := p.parseStep()
			if err != nil {
				return nil, err
			}
			wf.Steps[step.Name] = step
			if wf.EntryStep == "" {
				wf.EntryStep = step.Name
			}
		} else if p.current.Type == TokenIdent && p.peek.Type == TokenColon {
			wire, err := p.parseWire()
			if err != nil {
				return nil, err
			}
			wf.Wiring = append(wf.Wiring, wire)
		} else {
			return nil, fmt.Errorf("line %d col %d: unexpected token %q", p.current.Line, p.current.Col, p.current.Literal)
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	return wf, nil
}

func (p *Parser) parseStep() (*domain.Step, error) {
	p.advance() // consume "step"

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, fmt.Errorf("expected step name: %w", err)
	}

	if _, err := p.expect(TokenLParen); err != nil {
		return nil, err
	}

	typeTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, fmt.Errorf("expected step type: %w", err)
	}

	if _, err := p.expect(TokenRParen); err != nil {
		return nil, err
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	step := &domain.Step{
		Name:   nameTok.Literal,
		Type:   domain.StepType(typeTok.Literal),
		Config: make(map[string]string),
	}

	for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
		if err := p.parseStepField(step, ""); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	return step, nil
}

func (p *Parser) parseStepField(step *domain.Step, prefix string) error {
	keyTok, err := p.expect(TokenIdent)
	if err != nil {
		return fmt.Errorf("expected field name: %w", err)
	}

	key := keyTok.Literal
	if prefix != "" {
		key = prefix + "." + key
	}

	// Sub-block (e.g., container { ... })
	if p.current.Type == TokenLBrace {
		p.advance() // consume {
		for p.current.Type != TokenRBrace && p.current.Type != TokenEOF {
			if err := p.parseStepField(step, key); err != nil {
				return err
			}
		}
		p.advance() // consume }
		return nil
	}

	if _, err := p.expect(TokenEquals); err != nil {
		return err
	}

	if key == "results" {
		results, err := p.parseIdentList()
		if err != nil {
			return err
		}
		step.Results = results
	} else if p.current.Type == TokenLBracket {
		// String list (e.g., network_allow)
		values, err := p.parseStringList()
		if err != nil {
			return err
		}
		step.Config[key] = strings.Join(values, ",")
	} else {
		// Single value (string or ident expression like file("..."))
		val, err := p.parseValue()
		if err != nil {
			return err
		}
		step.Config[key] = val
	}

	return nil
}

func (p *Parser) parseIdentList() ([]string, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}

	var items []string
	for p.current.Type != TokenRBracket && p.current.Type != TokenEOF {
		tok, err := p.expect(TokenIdent)
		if err != nil {
			return nil, err
		}
		items = append(items, tok.Literal)
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return items, nil
}

func (p *Parser) parseStringList() ([]string, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}

	var items []string
	for p.current.Type != TokenRBracket && p.current.Type != TokenEOF {
		tok, err := p.expect(TokenString)
		if err != nil {
			return nil, err
		}
		items = append(items, tok.Literal)
		if p.current.Type == TokenComma {
			p.advance()
		}
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return items, nil
}

func (p *Parser) parseValue() (string, error) {
	if p.current.Type == TokenString {
		tok := p.current
		p.advance()
		return tok.Literal, nil
	}

	// Handle expressions like file("path") or step.code.output
	if p.current.Type == TokenIdent {
		var buf strings.Builder
		buf.WriteString(p.current.Literal)
		p.advance()

		// Function call: file("...")
		if p.current.Type == TokenLParen {
			buf.WriteRune('(')
			p.advance()
			for p.current.Type != TokenRParen && p.current.Type != TokenEOF {
				if p.current.Type == TokenString {
					buf.WriteRune('"')
					buf.WriteString(p.current.Literal)
					buf.WriteRune('"')
				} else {
					buf.WriteString(p.current.Literal)
				}
				p.advance()
			}
			buf.WriteRune(')')
			p.advance() // consume )
		}

		// Dotted path: step.code.output
		for p.current.Type == TokenDot {
			buf.WriteRune('.')
			p.advance()
			if p.current.Type == TokenIdent {
				buf.WriteString(p.current.Literal)
				p.advance()
			}
		}

		return buf.String(), nil
	}

	return "", fmt.Errorf("line %d col %d: expected value, got %q", p.current.Line, p.current.Col, p.current.Literal)
}

func (p *Parser) parseWire() (domain.Wire, error) {
	fromTok := p.current
	p.advance() // consume step name

	if _, err := p.expect(TokenColon); err != nil {
		return domain.Wire{}, err
	}

	resultTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	if _, err := p.expect(TokenArrow); err != nil {
		return domain.Wire{}, err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	return domain.Wire{
		From:   fromTok.Literal,
		Result: resultTok.Literal,
		To:     toTok.Literal,
	}, nil
}

func (p *Parser) expectIdent(value string) (Token, error) {
	if p.current.Type != TokenIdent || p.current.Literal != value {
		return p.current, fmt.Errorf("line %d col %d: expected %q, got %q",
			p.current.Line, p.current.Col, value, p.current.Literal)
	}
	tok := p.current
	p.advance()
	return tok, nil
}
```

**Step 4: Run tests**

```bash
go test ./internal/dsl/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add DSL parser for workflow files"
```

---

## Phase 4: Graph Engine

### Task 6: Graph Walker / Engine

**Files:**
- Create: `internal/engine/engine.go`
- Create: `internal/engine/engine_test.go`

**Step 1: Write engine tests**

`internal/engine/engine_test.go`:
```go
package engine_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExecutor records which steps were executed and returns predetermined results.
type fakeExecutor struct {
	results map[string]string // stepName -> result
	called  []string
}

func (f *fakeExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	f.called = append(f.called, step.Name)
	return f.results[step.Name], nil
}

func TestEngine_LinearWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		Name: "linear",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	exec := &fakeExecutor{results: map[string]string{"build": "success", "test": "pass"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, []string{"build", "test"}, exec.called)
}

func TestEngine_RetryLoop(t *testing.T) {
	wf := &domain.Workflow{
		Name: "retry",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
			"check": {Name: "check", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "code", Result: "fail", To: domain.StepAbort},
			{From: "check", Result: "pass", To: domain.StepDone},
			{From: "check", Result: "fail", To: "code"},
		},
		EntryStep: "code",
	}

	callCount := 0
	exec := &fakeExecutor{
		results: map[string]string{"code": "success", "check": "pass"},
	}
	// Override: first check returns "fail", second returns "pass"
	origExec := exec.Execute
	_ = origExec
	var dynamicExec engine.StepExecutor = engine.StepExecutorFunc(func(ctx context.Context, step *domain.Step) (string, error) {
		callCount++
		if step.Name == "check" && callCount <= 2 {
			return "fail", nil
		}
		return exec.results[step.Name], nil
	})

	eng := engine.New(dynamicExec)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
}

func TestEngine_Abort(t *testing.T) {
	wf := &domain.Workflow{
		Name: "abort-test",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "code", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{"code": "fail"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
}

func TestEngine_ContextCancellation(t *testing.T) {
	wf := &domain.Workflow{
		Name: "cancel-test",
		Steps: map[string]*domain.Step{
			"slow": {Name: "slow", Type: domain.StepTypeScript, Results: []string{"done"}},
		},
		Wiring: []domain.Wire{
			{From: "slow", Result: "done", To: domain.StepDone},
		},
		EntryStep: "slow",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	exec := &fakeExecutor{results: map[string]string{"slow": "done"}}
	eng := engine.New(exec)

	run, err := eng.Run(ctx, wf)
	require.Error(t, err)
	assert.Equal(t, domain.RunStateCancelled, run.State)
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/engine/ -v
```

**Step 3: Implement engine**

`internal/engine/engine.go`:
```go
package engine

import (
	"context"
	"fmt"

	"github.com/cloche-dev/cloche/internal/domain"
)

// StepExecutor executes a single step and returns the result name.
type StepExecutor interface {
	Execute(ctx context.Context, step *domain.Step) (string, error)
}

// StepExecutorFunc adapts a function to the StepExecutor interface.
type StepExecutorFunc func(ctx context.Context, step *domain.Step) (string, error)

func (f StepExecutorFunc) Execute(ctx context.Context, step *domain.Step) (string, error) {
	return f(ctx, step)
}

// StatusHandler receives notifications about workflow execution progress.
type StatusHandler interface {
	OnStepStart(run *domain.Run, step *domain.Step)
	OnStepComplete(run *domain.Run, step *domain.Step, result string)
	OnRunComplete(run *domain.Run)
}

// noopStatus is the default status handler that does nothing.
type noopStatus struct{}

func (noopStatus) OnStepStart(*domain.Run, *domain.Step)              {}
func (noopStatus) OnStepComplete(*domain.Run, *domain.Step, string)   {}
func (noopStatus) OnRunComplete(*domain.Run)                          {}

type Engine struct {
	executor StepExecutor
	status   StatusHandler
	maxSteps int // safety limit to prevent infinite loops
}

func New(executor StepExecutor) *Engine {
	return &Engine{
		executor: executor,
		status:   noopStatus{},
		maxSteps: 1000,
	}
}

func (e *Engine) SetStatusHandler(h StatusHandler) {
	e.status = h
}

func (e *Engine) SetMaxSteps(n int) {
	e.maxSteps = n
}

func (e *Engine) Run(ctx context.Context, wf *domain.Workflow) (*domain.Run, error) {
	if err := wf.Validate(); err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	run := domain.NewRun(generateRunID(), wf.Name)
	run.Start()

	currentStepName := wf.EntryStep
	stepCount := 0

	for currentStepName != domain.StepDone && currentStepName != domain.StepAbort {
		if err := ctx.Err(); err != nil {
			run.Complete(domain.RunStateCancelled)
			return run, fmt.Errorf("workflow cancelled: %w", err)
		}

		stepCount++
		if stepCount > e.maxSteps {
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("workflow exceeded maximum step count (%d)", e.maxSteps)
		}

		step, ok := wf.Steps[currentStepName]
		if !ok {
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("step %q not found in workflow", currentStepName)
		}

		run.RecordStepStart(step.Name)
		e.status.OnStepStart(run, step)

		result, err := e.executor.Execute(ctx, step)
		if err != nil {
			run.RecordStepComplete(step.Name, "error")
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("step %q execution failed: %w", step.Name, err)
		}

		run.RecordStepComplete(step.Name, result)
		e.status.OnStepComplete(run, step, result)

		nextStep, err := wf.NextStep(currentStepName, result)
		if err != nil {
			run.Complete(domain.RunStateFailed)
			return run, err
		}

		currentStepName = nextStep
	}

	if currentStepName == domain.StepDone {
		run.Complete(domain.RunStateSucceeded)
	} else {
		run.Complete(domain.RunStateFailed)
	}

	e.status.OnRunComplete(run)
	return run, nil
}

var runCounter int

func generateRunID() string {
	runCounter++
	return fmt.Sprintf("run-%d", runCounter)
}
```

**Step 4: Run tests**

```bash
go test ./internal/engine/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add graph engine with step execution and status hooks"
```

---

## Phase 5: Status Protocol

### Task 7: JSON-Lines Status Protocol

**Files:**
- Create: `internal/protocol/status.go`
- Create: `internal/protocol/status_test.go`

**Step 1: Write tests**

`internal/protocol/status_test.go`:
```go
package protocol_test

import (
	"bytes"
	"testing"

	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusWriter_WritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewStatusWriter(&buf)

	w.StepStarted("code")
	w.StepCompleted("code", "success")
	w.RunCompleted("succeeded")

	msgs, err := protocol.ParseStatusStream(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	assert.Equal(t, protocol.MsgStepStarted, msgs[0].Type)
	assert.Equal(t, "code", msgs[0].StepName)

	assert.Equal(t, protocol.MsgStepCompleted, msgs[1].Type)
	assert.Equal(t, "success", msgs[1].Result)

	assert.Equal(t, protocol.MsgRunCompleted, msgs[2].Type)
	assert.Equal(t, "succeeded", msgs[2].Result)
}

func TestStatusWriter_LogMessage(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewStatusWriter(&buf)

	w.Log("code", "running tests...")

	msgs, err := protocol.ParseStatusStream(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	assert.Equal(t, protocol.MsgLog, msgs[0].Type)
	assert.Equal(t, "running tests...", msgs[0].Message)
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/protocol/ -v
```

**Step 3: Implement**

`internal/protocol/status.go`:
```go
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type MessageType string

const (
	MsgStepStarted   MessageType = "step_started"
	MsgStepCompleted  MessageType = "step_completed"
	MsgRunCompleted   MessageType = "run_completed"
	MsgLog            MessageType = "log"
	MsgError          MessageType = "error"
)

type StatusMessage struct {
	Type      MessageType `json:"type"`
	StepName  string      `json:"step_name,omitempty"`
	Result    string      `json:"result,omitempty"`
	Message   string      `json:"message,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

type StatusWriter struct {
	w   io.Writer
	enc *json.Encoder
}

func NewStatusWriter(w io.Writer) *StatusWriter {
	return &StatusWriter{w: w, enc: json.NewEncoder(w)}
}

func (s *StatusWriter) StepStarted(stepName string) {
	s.write(StatusMessage{Type: MsgStepStarted, StepName: stepName})
}

func (s *StatusWriter) StepCompleted(stepName, result string) {
	s.write(StatusMessage{Type: MsgStepCompleted, StepName: stepName, Result: result})
}

func (s *StatusWriter) RunCompleted(result string) {
	s.write(StatusMessage{Type: MsgRunCompleted, Result: result})
}

func (s *StatusWriter) Log(stepName, message string) {
	s.write(StatusMessage{Type: MsgLog, StepName: stepName, Message: message})
}

func (s *StatusWriter) Error(stepName, message string) {
	s.write(StatusMessage{Type: MsgError, StepName: stepName, Message: message})
}

func (s *StatusWriter) write(msg StatusMessage) {
	msg.Timestamp = time.Now()
	_ = s.enc.Encode(msg)
}

func ParseStatusStream(data []byte) ([]StatusMessage, error) {
	var msgs []StatusMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var msg StatusMessage
		if err := dec.Decode(&msg); err != nil {
			return msgs, fmt.Errorf("failed to decode status message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}
```

**Step 4: Run tests**

```bash
go test ./internal/protocol/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add JSON-lines status protocol for agent-daemon communication"
```

---

## Phase 6: Ports

### Task 8: Define Port Interfaces

**Files:**
- Create: `internal/ports/store.go`
- Create: `internal/ports/container.go`
- Create: `internal/ports/agent.go`

**Step 1: Define interfaces (no tests needed — pure interfaces)**

`internal/ports/store.go`:
```go
package ports

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

type RunStore interface {
	CreateRun(ctx context.Context, run *domain.Run) error
	GetRun(ctx context.Context, id string) (*domain.Run, error)
	UpdateRun(ctx context.Context, run *domain.Run) error
	ListRuns(ctx context.Context) ([]*domain.Run, error)
}

type CaptureStore interface {
	SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error
	GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error)
}
```

`internal/ports/container.go`:
```go
package ports

import (
	"context"
	"io"
)

type ContainerConfig struct {
	Image        string
	WorkflowName string
	ProjectDir   string
	NetworkAllow []string
	GitRemote    string
}

type ContainerRuntime interface {
	Start(ctx context.Context, cfg ContainerConfig) (containerID string, err error)
	Stop(ctx context.Context, containerID string) error
	AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error)
	Wait(ctx context.Context, containerID string) (exitCode int, err error)
}
```

`internal/ports/agent.go`:
```go
package ports

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

// AgentAdapter executes a single agent step inside the container.
// It translates between Cloche's step protocol and the specific agent's interface.
type AgentAdapter interface {
	// Name returns the adapter identifier (e.g., "claudecode", "generic").
	Name() string

	// Execute runs the agent for the given step and returns the result name.
	Execute(ctx context.Context, step *domain.Step, workDir string) (result string, err error)
}
```

**Step 2: Verify compilation**

```bash
go build ./internal/ports/
```
Expected: success

**Step 3: Commit**

```bash
git add -A && git commit -m "feat: define port interfaces (store, container, agent)"
```

---

## Phase 7: Agent Adapters

### Task 9: Generic Agent Adapter (Script Executor)

**Files:**
- Create: `internal/adapters/agents/generic/generic.go`
- Create: `internal/adapters/agents/generic/generic_test.go`

**Step 1: Write tests**

`internal/adapters/agents/generic/generic_test.go`:
```go
package generic_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericAdapter_ScriptSuccess(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()

	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
}

func TestGenericAdapter_ScriptFailure(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()

	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "exit 1"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
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

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "generated")
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/adapters/agents/generic/ -v
```

**Step 3: Implement**

`internal/adapters/agents/generic/generic.go`:
```go
package generic

import (
	"context"
	"os/exec"

	"github.com/cloche-dev/cloche/internal/domain"
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

	if err := cmd.Run(); err != nil {
		// Non-zero exit = "fail" result (not an error — the step ran, it just failed)
		if _, ok := err.(*exec.ExitError); ok {
			return resultOrDefault(step.Results, "fail"), nil
		}
		return "", err
	}

	return resultOrDefault(step.Results, "success"), nil
}

// resultOrDefault returns the named result if it exists in the step's declared results,
// otherwise returns the first result.
func resultOrDefault(results []string, name string) string {
	for _, r := range results {
		if r == name {
			return r
		}
	}
	if len(results) > 0 {
		return results[0]
	}
	return name
}
```

**Step 4: Run tests**

```bash
go test ./internal/adapters/agents/generic/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add generic agent adapter for script execution"
```

---

## Phase 8: cloche-agent Binary

### Task 10: Wire cloche-agent Together

This is the first vertical slice — `cloche-agent` can parse a workflow file and execute it locally.

**Files:**
- Modify: `cmd/cloche-agent/main.go`
- Create: `internal/agent/runner.go`
- Create: `internal/agent/runner_test.go`
- Create: `testdata/workflows/simple.cloche`

**Step 1: Create test workflow file**

`testdata/workflows/simple.cloche`:
```
workflow "simple-build" {
  step build(script) {
    run = "echo 'building...'"
    results = [success, fail]
  }

  step test(script) {
    run = "echo 'testing...'"
    results = [pass, fail]
  }

  build:success -> test
  build:fail -> abort

  test:pass -> done
  test:fail -> build
}
```

**Step 2: Write runner tests**

`internal/agent/runner_test.go`:
```go
package agent_test

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

func TestRunner_ExecutesWorkflowFile(t *testing.T) {
	workflowPath := "../../testdata/workflows/simple.cloche"
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		t.Skip("test workflow file not found")
	}

	var statusBuf bytes.Buffer
	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      t.TempDir(),
		StatusOutput: &statusBuf,
	})

	err := runner.Run(context.Background())
	require.NoError(t, err)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)

	// Should have: build started, build completed, test started, test completed, run completed
	assert.GreaterOrEqual(t, len(msgs), 5)

	// Last message should be run completed with succeeded
	last := msgs[len(msgs)-1]
	assert.Equal(t, protocol.MsgRunCompleted, last.Type)
	assert.Equal(t, "succeeded", last.Result)
}
```

**Step 3: Run tests — expect failure**

```bash
go test ./internal/agent/ -v
```

**Step 4: Implement runner**

`internal/agent/runner.go`:
```go
package agent

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/protocol"
)

type RunnerConfig struct {
	WorkflowPath string
	WorkDir      string
	StatusOutput io.Writer
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	// Parse workflow
	data, err := os.ReadFile(r.cfg.WorkflowPath)
	if err != nil {
		return fmt.Errorf("reading workflow file: %w", err)
	}

	wf, err := dsl.Parse(string(data))
	if err != nil {
		return fmt.Errorf("parsing workflow: %w", err)
	}

	// Set up status writer
	statusWriter := protocol.NewStatusWriter(r.cfg.StatusOutput)

	// Set up executor with generic adapter for script steps
	genericAdapter := generic.New()
	executor := &stepExecutor{
		workDir: r.cfg.WorkDir,
		generic: genericAdapter,
		status:  statusWriter,
	}

	// Run engine
	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter})

	run, err := eng.Run(ctx, wf)
	if err != nil {
		statusWriter.Error("", err.Error())
		statusWriter.RunCompleted("failed")
		return err
	}

	statusWriter.RunCompleted(string(run.State))
	return nil
}

type stepExecutor struct {
	workDir string
	generic *generic.Adapter
	status  *protocol.StatusWriter
}

func (e *stepExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	switch step.Type {
	case domain.StepTypeScript:
		return e.generic.Execute(ctx, step, e.workDir)
	case domain.StepTypeAgent:
		// For MVP, agent steps also use the generic adapter if a "run" config is set.
		// Full agent adapter support comes in a later task.
		if _, ok := step.Config["run"]; ok {
			return e.generic.Execute(ctx, step, e.workDir)
		}
		return "", fmt.Errorf("agent step %q requires an agent adapter (not yet implemented for prompt-based steps)", step.Name)
	default:
		return "", fmt.Errorf("unknown step type: %s", step.Type)
	}
}

type statusReporter struct {
	writer *protocol.StatusWriter
}

func (s *statusReporter) OnStepStart(run *domain.Run, step *domain.Step) {
	s.writer.StepStarted(step.Name)
}

func (s *statusReporter) OnStepComplete(run *domain.Run, step *domain.Step, result string) {
	s.writer.StepCompleted(step.Name, result)
}

func (s *statusReporter) OnRunComplete(run *domain.Run) {
	// Handled by Runner.Run directly
}
```

**Step 5: Update cloche-agent main**

`cmd/cloche-agent/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloche-dev/cloche/internal/agent"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cloche-agent <workflow-file>\n")
		os.Exit(1)
	}

	workflowPath := os.Args[1]
	workDir, _ := os.Getwd()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGTERM for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      workDir,
		StatusOutput: os.Stdout,
	})

	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
```

**Step 6: Run tests**

```bash
go test ./internal/agent/ -v
```
Expected: all PASS

**Step 7: Build and smoke test**

```bash
make build
./bin/cloche-agent testdata/workflows/simple.cloche
```
Expected: JSON-lines output showing step_started, step_completed, run_completed

**Step 8: Commit**

```bash
git add -A && git commit -m "feat: wire cloche-agent with DSL parser, engine, and status output"
```

---

## Phase 9: SQLite Storage

### Task 11: SQLite RunStore Adapter

**Files:**
- Create: `internal/adapters/sqlite/store.go`
- Create: `internal/adapters/sqlite/store_test.go`

**Step 1: Write tests**

`internal/adapters/sqlite/store_test.go`:
```go
package sqlite_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStore_CreateAndGet(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", got.ID)
	assert.Equal(t, "test-workflow", got.WorkflowName)
	assert.Equal(t, domain.RunStateRunning, got.State)
}

func TestRunStore_Update(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.UpdateRun(ctx, run))

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, got.State)
}

func TestRunStore_List(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run1 := domain.NewRun("run-1", "wf-a")
	run2 := domain.NewRun("run-2", "wf-b")
	require.NoError(t, store.CreateRun(ctx, run1))
	require.NoError(t, store.CreateRun(ctx, run2))

	runs, err := store.ListRuns(ctx)
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestRunStore_GetNotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	_, err = store.GetRun(context.Background(), "nonexistent")
	assert.Error(t, err)
}
```

**Step 2: Run tests — expect failure**

```bash
go test ./internal/adapters/sqlite/ -v
```

**Step 3: Implement**

`internal/adapters/sqlite/store.go`:
```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			workflow_name TEXT NOT NULL,
			state TEXT NOT NULL,
			current_step TEXT,
			started_at TEXT,
			completed_at TEXT
		);
		CREATE TABLE IF NOT EXISTS step_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			step_name TEXT NOT NULL,
			result TEXT,
			started_at TEXT NOT NULL,
			completed_at TEXT,
			logs TEXT,
			git_ref TEXT,
			FOREIGN KEY (run_id) REFERENCES runs(id)
		);
	`)
	return err
}

func (s *Store) CreateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow_name, state, current_step, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.CurrentStep,
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
	)
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, state, current_step, started_at, completed_at FROM runs WHERE id = ?`, id)

	run := &domain.Run{}
	var startedAt, completedAt string
	err := row.Scan(&run.ID, &run.WorkflowName, &run.State, &run.CurrentStep, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", id)
	}
	if err != nil {
		return nil, err
	}

	run.StartedAt = parseTime(startedAt)
	run.CompletedAt = parseTime(completedAt)
	return run, nil
}

func (s *Store) UpdateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = ?, current_step = ?, started_at = ?, completed_at = ? WHERE id = ?`,
		string(run.State), run.CurrentStep,
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
		run.ID,
	)
	return err
}

func (s *Store) ListRuns(ctx context.Context) ([]*domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow_name, state, current_step, started_at, completed_at FROM runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.Run
	for rows.Next() {
		run := &domain.Run{}
		var startedAt, completedAt string
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.State, &run.CurrentStep, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		run.StartedAt = parseTime(startedAt)
		run.CompletedAt = parseTime(completedAt)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO step_executions (run_id, step_name, result, started_at, completed_at, logs, git_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID, exec.StepName, exec.Result,
		formatTime(exec.StartedAt), formatTime(exec.CompletedAt),
		exec.Logs, exec.GitRef,
	)
	return err
}

func (s *Store) GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT step_name, result, started_at, completed_at, logs, git_ref
		 FROM step_executions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*domain.StepExecution
	for rows.Next() {
		e := &domain.StepExecution{}
		var startedAt, completedAt, logs, gitRef string
		if err := rows.Scan(&e.StepName, &e.Result, &startedAt, &completedAt, &logs, &gitRef); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(startedAt)
		e.CompletedAt = parseTime(completedAt)
		e.Logs = logs
		e.GitRef = gitRef
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
```

**Step 4: Install dependency and run tests**

```bash
go get modernc.org/sqlite
go test ./internal/adapters/sqlite/ -v
```
Expected: all PASS

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add SQLite storage adapter for runs and captures"
```

---

## Phase 10: gRPC API

### Task 12: Protobuf Definitions

**Files:**
- Create: `api/proto/cloche/v1/cloche.proto`
- Create: `buf.gen.yaml` (or direct protoc invocation in Makefile)

**Step 1: Install protoc tooling**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

**Step 2: Create proto file**

`api/proto/cloche/v1/cloche.proto`:
```protobuf
syntax = "proto3";

package cloche.v1;

option go_package = "github.com/cloche-dev/cloche/api/clochepb";

service ClocheService {
  rpc RunWorkflow(RunWorkflowRequest) returns (RunWorkflowResponse);
  rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);
  rpc StreamLogs(StreamLogsRequest) returns (stream LogEntry);
  rpc StopRun(StopRunRequest) returns (StopRunResponse);
  rpc ListRuns(ListRunsRequest) returns (ListRunsResponse);
}

message RunWorkflowRequest {
  string workflow_name = 1;
  string project_dir = 2;
  string image = 3;
}

message RunWorkflowResponse {
  string run_id = 1;
}

message GetStatusRequest {
  string run_id = 1;
}

message GetStatusResponse {
  string run_id = 1;
  string workflow_name = 2;
  string state = 3;
  string current_step = 4;
  repeated StepExecutionStatus step_executions = 5;
}

message StepExecutionStatus {
  string step_name = 1;
  string result = 2;
  string started_at = 3;
  string completed_at = 4;
}

message StreamLogsRequest {
  string run_id = 1;
}

message LogEntry {
  string type = 1;
  string step_name = 2;
  string result = 3;
  string message = 4;
  string timestamp = 5;
}

message StopRunRequest {
  string run_id = 1;
}

message StopRunResponse {}

message ListRunsRequest {}

message ListRunsResponse {
  repeated RunSummary runs = 1;
}

message RunSummary {
  string run_id = 1;
  string workflow_name = 2;
  string state = 3;
  string started_at = 4;
}
```

**Step 3: Add proto generation to Makefile**

Add to Makefile:
```makefile
proto:
	mkdir -p api/clochepb
	protoc --proto_path=api/proto \
		--go_out=api/clochepb --go_opt=paths=source_relative \
		--go-grpc_out=api/clochepb --go-grpc_opt=paths=source_relative \
		api/proto/cloche/v1/cloche.proto
```

**Step 4: Generate**

```bash
make proto
```

**Step 5: Install gRPC dependency**

```bash
go get google.golang.org/grpc
go get google.golang.org/protobuf
```

**Step 6: Commit**

```bash
git add -A && git commit -m "feat: add protobuf API definitions for cloche gRPC service"
```

### Task 13: gRPC Server (Daemon Side)

**Files:**
- Create: `internal/adapters/grpc/server.go`
- Create: `internal/adapters/grpc/server_test.go`

**Step 1: Write tests**

`internal/adapters/grpc/server_test.go`:
```go
package grpc_test

import (
	"context"
	"testing"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_ListRuns_Empty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(context.Background(), &pb.ListRunsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Runs)
}

func TestServer_GetStatus_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: "nonexistent"})
	assert.Error(t, err)
}
```

**Step 2: Implement server**

`internal/adapters/grpc/server.go`:
```go
package grpc

import (
	"context"
	"fmt"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/ports"
)

type ClocheServer struct {
	pb.UnimplementedClocheServiceServer
	store     ports.RunStore
	container ports.ContainerRuntime
}

func NewClocheServer(store ports.RunStore, container ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{store: store, container: container}
}

func (s *ClocheServer) ListRuns(ctx context.Context, req *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	runs, err := s.store.ListRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	resp := &pb.ListRunsResponse{}
	for _, run := range runs {
		resp.Runs = append(resp.Runs, &pb.RunSummary{
			RunId:        run.ID,
			WorkflowName: run.WorkflowName,
			State:        string(run.State),
			StartedAt:    run.StartedAt.String(),
		})
	}
	return resp, nil
}

func (s *ClocheServer) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}

	resp := &pb.GetStatusResponse{
		RunId:        run.ID,
		WorkflowName: run.WorkflowName,
		State:        string(run.State),
		CurrentStep:  run.CurrentStep,
	}

	for _, exec := range run.StepExecutions {
		resp.StepExecutions = append(resp.StepExecutions, &pb.StepExecutionStatus{
			StepName:    exec.StepName,
			Result:      exec.Result,
			StartedAt:   exec.StartedAt.String(),
			CompletedAt: exec.CompletedAt.String(),
		})
	}

	return resp, nil
}

func (s *ClocheServer) StopRun(ctx context.Context, req *pb.StopRunRequest) (*pb.StopRunResponse, error) {
	// Will be implemented with container runtime integration
	return &pb.StopRunResponse{}, nil
}
```

**Step 3: Run tests**

```bash
go test ./internal/adapters/grpc/ -v
```
Expected: all PASS

**Step 4: Commit**

```bash
git add -A && git commit -m "feat: add gRPC server with list/status/stop endpoints"
```

---

## Phase 11: Docker Adapter

### Task 14: Docker Container Runtime Adapter

**Files:**
- Create: `internal/adapters/docker/runtime.go`
- Create: `internal/adapters/docker/runtime_test.go`

Note: Docker tests require Docker to be running. Use build tags or skip logic.

**Step 1: Write tests**

`internal/adapters/docker/runtime_test.go`:
```go
package docker_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}
}

func TestDockerRuntime_StartAndStop(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   t.TempDir(),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)

	err = rt.Stop(ctx, containerID)
	assert.NoError(t, err)
}
```

**Step 2: Implement**

`internal/adapters/docker/runtime.go`:
```go
package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/cloche-dev/cloche/internal/ports"
)

type Runtime struct {
	client *client.Client
}

func NewRuntime() (*Runtime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return &Runtime{client: cli}, nil
}

func (r *Runtime) Start(ctx context.Context, cfg ports.ContainerConfig) (string, error) {
	// Pull image if needed
	reader, err := r.client.ImagePull(ctx, cfg.Image, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pulling image %s: %w", cfg.Image, err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	// Create container
	networkMode := "none"
	if len(cfg.NetworkAllow) > 0 {
		// TODO: Create custom network with allowlist. For now, use bridge.
		networkMode = "bridge"
	}

	resp, err := r.client.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Cmd:        []string{"cloche-agent", cfg.WorkflowName},
			WorkingDir: "/workspace",
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(networkMode),
		},
		nil, nil, "",
	)
	if err != nil {
		return "", fmt.Errorf("creating container: %w", err)
	}

	// Copy project files in
	// TODO: Implement project file copy via docker cp

	// Start container
	if err := r.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting container: %w", err)
	}

	return resp.ID, nil
}

func (r *Runtime) Stop(ctx context.Context, containerID string) error {
	return r.client.ContainerStop(ctx, containerID, container.StopOptions{})
}

func (r *Runtime) AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error) {
	reader, err := r.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("attaching to container output: %w", err)
	}
	return reader, nil
}

func (r *Runtime) Wait(ctx context.Context, containerID string) (int, error) {
	waitCh, errCh := r.client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case result := <-waitCh:
		return int(result.StatusCode), nil
	case err := <-errCh:
		return -1, err
	}
}
```

**Step 3: Install Docker SDK**

```bash
go get github.com/docker/docker/client
go get github.com/docker/docker/api/types
```

**Step 4: Run tests**

```bash
go test ./internal/adapters/docker/ -v
```
Expected: PASS (or SKIP if Docker not available)

**Step 5: Commit**

```bash
git add -A && git commit -m "feat: add Docker container runtime adapter"
```

---

## Phase 12: Daemon Binary

### Task 15: Wire cloched Together

**Files:**
- Modify: `cmd/cloched/main.go`

**Step 1: Implement daemon main**

`cmd/cloched/main.go`:
```go
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"google.golang.org/grpc"
)

func main() {
	dbPath := os.Getenv("CLOCHE_DB")
	if dbPath == "" {
		dbPath = "cloche.db"
	}

	listenAddr := os.Getenv("CLOCHE_LISTEN")
	if listenAddr == "" {
		listenAddr = "unix:///tmp/cloche.sock"
	}

	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	srv := adaptgrpc.NewClocheServer(store, nil)

	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	lis, err := listen(listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen on %s: %v\n", listenAddr, err)
		os.Exit(1)
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		grpcServer.GracefulStop()
	}()

	fmt.Fprintf(os.Stderr, "cloched listening on %s\n", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func listen(addr string) (net.Listener, error) {
	if len(addr) > 7 && addr[:7] == "unix://" {
		sockPath := addr[7:]
		os.Remove(sockPath) // clean up stale socket
		return net.Listen("unix", sockPath)
	}
	return net.Listen("tcp", addr)
}
```

**Step 2: Verify build**

```bash
make build
```
Expected: all three binaries compile

**Step 3: Commit**

```bash
git add -A && git commit -m "feat: wire cloched daemon with gRPC server and SQLite store"
```

---

## Phase 13: CLI Client

### Task 16: Wire cloche CLI

**Files:**
- Modify: `cmd/cloche/main.go`

**Step 1: Implement CLI**

`cmd/cloche/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = "unix:///tmp/cloche.sock"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch os.Args[1] {
	case "run":
		cmdRun(ctx, client, os.Args[2:])
	case "status":
		cmdStatus(ctx, client, os.Args[2:])
	case "list":
		cmdList(ctx, client)
	case "stop":
		cmdStop(ctx, client, os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: cloche <command> [args]\n\nCommands:\n  run <workflow>  Launch a workflow run\n  status <run-id> Check run status\n  list            List all runs\n  stop <run-id>   Stop a running workflow\n")
}

func cmdRun(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche run <workflow-name>\n")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName: args[0],
		ProjectDir:   cwd,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Started run: %s\n", resp.RunId)
}

func cmdStatus(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche status <run-id>\n")
		os.Exit(1)
	}

	resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{RunId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Run:      %s\n", resp.RunId)
	fmt.Printf("Workflow: %s\n", resp.WorkflowName)
	fmt.Printf("State:    %s\n", resp.State)
	fmt.Printf("Step:     %s\n", resp.CurrentStep)
	for _, exec := range resp.StepExecutions {
		fmt.Printf("  %s: %s (%s -> %s)\n", exec.StepName, exec.Result, exec.StartedAt, exec.CompletedAt)
	}
}

func cmdList(ctx context.Context, client pb.ClocheServiceClient) {
	resp, err := client.ListRuns(ctx, &pb.ListRunsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Runs) == 0 {
		fmt.Println("No runs found.")
		return
	}

	for _, run := range resp.Runs {
		fmt.Printf("%s  %-20s  %s  %s\n", run.RunId, run.WorkflowName, run.State, run.StartedAt)
	}
}

func cmdStop(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche stop <run-id>\n")
		os.Exit(1)
	}

	_, err := client.StopRun(ctx, &pb.StopRunRequest{RunId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Stopped run: %s\n", args[0])
}
```

**Step 2: Build all**

```bash
make build
```
Expected: all three binaries compile

**Step 3: Commit**

```bash
git add -A && git commit -m "feat: wire cloche CLI with run/status/list/stop subcommands"
```

---

## Phase 14: Integration Test

### Task 17: End-to-End Smoke Test

**Files:**
- Create: `test/integration/smoke_test.go`

**Step 1: Write integration test**

`test/integration/smoke_test.go`:
```go
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
	// Write a workflow file to a temp dir
	dir := t.TempDir()
	workflowContent := `workflow "smoke-test" {
  step build(script) {
    run = "echo building && echo 'built' > built.txt"
    results = [success, fail]
  }

  step verify(script) {
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
```

**Step 2: Run integration test**

```bash
go test ./test/integration/ -v
```
Expected: PASS

**Step 3: Commit**

```bash
git add -A && git commit -m "test: add end-to-end smoke test for agent workflow execution"
```

---

## Summary

| Phase | Tasks | What It Delivers |
|-------|-------|------------------|
| 1 | 1 | Go module, directory structure, Makefile |
| 2 | 2-3 | Domain types (Workflow, Step, Run, Capture) |
| 3 | 4-5 | DSL lexer and parser |
| 4 | 6 | Graph engine with step execution |
| 5 | 7 | JSON-lines status protocol |
| 6 | 8 | Port interfaces |
| 7 | 9 | Generic agent adapter (script execution) |
| 8 | 10 | **cloche-agent binary** (first vertical slice!) |
| 9 | 11 | SQLite storage adapter |
| 10 | 12-13 | gRPC API (proto + server) |
| 11 | 14 | Docker container runtime adapter |
| 12 | 15 | **cloched daemon binary** |
| 13 | 16 | **cloche CLI binary** |
| 14 | 17 | End-to-end integration test |
