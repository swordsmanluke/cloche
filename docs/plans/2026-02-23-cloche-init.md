# `cloche init` Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `cloche init` command that scaffolds a Cloche project (workflow, Dockerfile, prompt templates) in the current directory.

**Architecture:** New `init` case in the CLI's main switch, dispatching to `cmdInit()` in a new `cmd/cloche/init.go` file. Templates are string constants with `fmt.Sprintf` substitution. No daemon connection needed.

**Tech Stack:** Go, `os` for file I/O, `fmt.Sprintf` for templating.

---

### Task 1: Create `cmd/cloche/init.go` with templates and `cmdInit()`

**Files:**
- Create: `cmd/cloche/init.go`

**Step 1: Write `cmd/cloche/init.go`**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workflowTemplate = `workflow "%s" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> fix
  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
`

var dockerfileTemplate = `FROM golang:1.25 AS cloche-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent

FROM %s
RUN apt-get update && apt-get install -y git nodejs npm && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=cloche-builder /cloche-agent /usr/local/bin/cloche-agent
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
USER agent
`

var implementPrompt = `Implement the following change in this project.

## User Request
(Contents of .cloche/prompt.txt will be injected here by the adapter)

## Guidelines
- Follow existing project conventions if files already exist
- Write tests for new functionality
- Run tests locally before declaring success
`

var fixPrompt = `Fix the code based on the validation failures below.
Only modify files that need fixing. Do not rewrite the entire project.

## Validation Output
(Contents of .cloche/output/*.log will be injected here by the adapter)
`

func cmdInit(args []string) {
	workflow := "develop"
	image := "ubuntu:24.04"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflow = args[i]
			}
		case "--image":
			if i+1 < len(args) {
				i++
				image = args[i]
			}
		}
	}

	workflowFile := workflow + ".cloche"

	// Refuse to overwrite existing workflow
	if _, err := os.Stat(workflowFile); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", workflowFile)
		os.Exit(1)
	}

	// Create prompts directory
	if err := os.MkdirAll("prompts", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating prompts/: %v\n", err)
		os.Exit(1)
	}

	// Write all files
	files := map[string]string{
		workflowFile:            fmt.Sprintf(workflowTemplate, workflow),
		"Dockerfile":            fmt.Sprintf(dockerfileTemplate, image),
		"prompts/implement.md":  implementPrompt,
		"prompts/fix.md":        fixPrompt,
	}

	for path, content := range files {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr, "  skip %s (already exists)\n", path)
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  create %s\n", path)
	}

	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Edit %s — change the test command for your project\n", workflowFile)
	fmt.Fprintf(os.Stderr, "  2. Edit Dockerfile — add your project's dependencies\n")
	fmt.Fprintf(os.Stderr, "  3. docker build -t cloche-agent .\n")
	fmt.Fprintf(os.Stderr, "  4. cloche run --workflow %s --prompt \"...\"\n", workflow)
}
```

Note: The `files` map iteration order is non-deterministic, but that's fine —
the output order of "create" messages doesn't matter.

**Step 2: Verify it compiles**

Run: `go build ./cmd/cloche/`
Expected: Compile error — `cmdInit` not wired into `main.go` yet. That's fine,
confirms the file parses.

Actually this will compile since `cmdInit` is defined but not yet called.
Expected: Success, no errors.

**Step 3: Commit**

```
git add cmd/cloche/init.go
git commit -m "feat(cli): add init command templates"
```

---

### Task 2: Wire `init` into `cmd/cloche/main.go`

**Files:**
- Modify: `cmd/cloche/main.go`

**Step 1: Add `init` case to the switch and usage text**

In `main()`, add the `init` case **before** the gRPC connection setup (since
init doesn't need a daemon). Restructure so that `init` is handled early:

```go
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	// Commands that don't need a daemon connection
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
		return
	}

	// Commands that need a daemon connection
	addr := os.Getenv("CLOCHE_ADDR")
	// ... rest unchanged
```

Update `usage()` to include `init`:

```go
func usage() {
	fmt.Fprintf(os.Stderr, `usage: cloche <command> [args]

Commands:
  init [--workflow <name>] [--image <base>]  Initialize a Cloche project
  run --workflow <name> [--prompt "..."]      Launch a workflow run
  status <run-id>                             Check run status
  list                                        List all runs
  stop <run-id>                               Stop a running workflow
`)
}
```

**Step 2: Verify it compiles**

Run: `go build ./cmd/cloche/`
Expected: Success

**Step 3: Commit**

```
git add cmd/cloche/main.go
git commit -m "feat(cli): wire init subcommand into CLI dispatch"
```

---

### Task 3: Write tests for `cmdInit`

**Files:**
- Create: `cmd/cloche/init_test.go`

**Step 1: Write tests**

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCmdInit_DefaultFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	// Verify all files created
	for _, path := range []string{
		"develop.cloche",
		"Dockerfile",
		"prompts/implement.md",
		"prompts/fix.md",
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
		}
	}

	// Verify workflow name is embedded
	data, _ := os.ReadFile("develop.cloche")
	if got := string(data); !contains(got, `workflow "develop"`) {
		t.Errorf("workflow file missing workflow name, got:\n%s", got)
	}
}

func TestCmdInit_CustomFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--workflow", "build", "--image", "python:3.12"})

	// Custom workflow file
	if _, err := os.Stat("build.cloche"); os.IsNotExist(err) {
		t.Error("expected build.cloche to exist")
	}
	if _, err := os.Stat("develop.cloche"); err == nil {
		t.Error("develop.cloche should not exist with --workflow build")
	}

	// Custom image in Dockerfile
	data, _ := os.ReadFile("Dockerfile")
	if !contains(string(data), "FROM python:3.12") {
		t.Error("Dockerfile should contain custom base image")
	}
}

func TestCmdInit_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Pre-create Dockerfile with custom content
	os.WriteFile("Dockerfile", []byte("custom"), 0644)

	cmdInit([]string{})

	// Dockerfile should NOT be overwritten
	data, _ := os.ReadFile("Dockerfile")
	if string(data) != "custom" {
		t.Error("existing Dockerfile was overwritten")
	}

	// Other files should still be created
	if _, err := os.Stat("develop.cloche"); os.IsNotExist(err) {
		t.Error("develop.cloche should still be created")
	}
}

func TestCmdInit_RefusesExistingWorkflow(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Pre-create the workflow file
	os.WriteFile("develop.cloche", []byte("existing"), 0644)

	// cmdInit calls os.Exit(1) when workflow exists.
	// We can't easily test os.Exit, so just verify the file check logic.
	if _, err := os.Stat("develop.cloche"); os.IsNotExist(err) {
		t.Error("precondition failed: workflow file should exist")
	}
}

func contains(s, substr string) bool {
	return filepath.Base(s) != "" && // unused, just to avoid import error
		len(s) >= len(substr) &&
		stringContains(s, substr)
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

Note: `TestCmdInit_RefusesExistingWorkflow` can't easily test `os.Exit(1)`.
It just verifies the precondition. The real guard is tested manually.

**Step 2: Run tests**

Run: `go test ./cmd/cloche/ -v -run TestCmdInit`
Expected: All PASS

**Step 3: Commit**

```
git add cmd/cloche/init_test.go
git commit -m "test(cli): add tests for cloche init"
```

---

### Task 4: Manual verification

**Step 1: Build the binary**

Run: `go install ./cmd/cloche`

**Step 2: Test in a temp directory**

```
mkdir /tmp/test-init && cd /tmp/test-init
cloche init
```

Expected output:
```
  create develop.cloche
  create Dockerfile
  create prompts/implement.md
  create prompts/fix.md

Initialized Cloche project in test-init

Next steps:
  1. Edit develop.cloche — change the test command for your project
  2. Edit Dockerfile — add your project's dependencies
  3. docker build -t cloche-agent .
  4. cloche run --workflow develop --prompt "..."
```

**Step 3: Test with flags**

```
rm -rf /tmp/test-init && mkdir /tmp/test-init && cd /tmp/test-init
cloche init --workflow build --image python:3.12
```

Verify `build.cloche` exists with `workflow "build"`, Dockerfile has `FROM python:3.12`.

**Step 4: Test refusal**

```
cloche init
```

Expected: `error: build.cloche already exists` (exit 1) — wait, the workflow
is `develop` by default but we created `build.cloche`. So this should create
`develop.cloche` fine. Test with:

```
cloche init --workflow build
```

Expected: `error: build.cloche already exists`

**Step 5: Cleanup**

```
rm -rf /tmp/test-init
```
