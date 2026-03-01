# Subdirectory Layout, Daemon-Side Extraction, and Poll Command — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move cloche project files into a `./cloche/` subdirectory, replace in-container git push with daemon-side extraction via `docker cp` + git worktrees, and add a `poll` CLI command.

**Architecture:** The host project root contains user-facing files (.git, CLAUDE.md). A `./cloche/` subdirectory holds all cloche-managed files (workflow, Dockerfile, agent CLAUDE.md, source code). The daemon copies `./cloche/` into containers and extracts results using `docker cp` + git worktrees for concurrent branch creation. A new `poll` command uses gRPC polling to stream status updates until terminal.

**Tech Stack:** Go, gRPC/protobuf, Docker CLI, git CLI

---

## Task 1: Add `BaseSHA` field to domain and persistence

**Files:**
- Modify: `internal/domain/run.go:31-42`
- Modify: `internal/adapters/sqlite/store.go:46-100` (migrate + CRUD)

**Step 1: Add BaseSHA to Run struct**

In `internal/domain/run.go`, add the `BaseSHA` field to the `Run` struct:

```go
type Run struct {
	ID             string
	WorkflowName   string
	State          RunState
	ActiveSteps    []string
	StepExecutions []*StepExecution
	StartedAt      time.Time
	CompletedAt    time.Time
	ProjectDir     string
	ErrorMessage   string
	ContainerID    string
	BaseSHA        string // Git HEAD at run start, for result branch creation
}
```

**Step 2: Add migration in SQLite store**

In `internal/adapters/sqlite/store.go`, add to the `alterStmts` slice in `migrate()`:

```go
`ALTER TABLE runs ADD COLUMN base_sha TEXT`,
```

**Step 3: Update CreateRun to persist BaseSHA**

In `internal/adapters/sqlite/store.go`, update the `CreateRun` INSERT to include `base_sha`:

```go
func (s *Store) CreateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir, error_message, container_id, base_sha)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt), run.ProjectDir, run.ErrorMessage, run.ContainerID, run.BaseSHA,
	)
	return err
}
```

**Step 4: Update GetRun to read BaseSHA**

Update the SELECT and Scan in `GetRun`:

```go
func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, state, active_steps, started_at, completed_at, project_dir, COALESCE(error_message,''), COALESCE(container_id,''), COALESCE(base_sha,'') FROM runs WHERE id = ?`, id)

	run := &domain.Run{}
	var activeSteps, startedAt, completedAt string
	err := row.Scan(&run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir, &run.ErrorMessage, &run.ContainerID, &run.BaseSHA)
	// ... rest unchanged
```

**Step 5: Update ListRuns and ListRunsSince similarly**

Add `COALESCE(base_sha,'')` to the SELECT and `&run.BaseSHA` to the Scan in both `ListRuns` and `ListRunsSince`.

**Step 6: Run tests**

Run: `go build ./... && go test ./internal/adapters/sqlite/...`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/domain/run.go internal/adapters/sqlite/store.go
git commit -m "feat: add BaseSHA field to Run for result branch creation"
```

---

## Task 2: Remove git daemon from Docker adapter

**Files:**
- Modify: `internal/adapters/docker/runtime.go`

**Step 1: Remove gitDaemons tracking from Runtime struct**

Replace the Runtime struct and NewRuntime:

```go
type Runtime struct{}

func NewRuntime() (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found in PATH: %w", err)
	}
	return &Runtime{}, nil
}
```

**Step 2: Remove git daemon start from Start()**

Remove lines 34-64 (gitRepoRoot lookup, git daemon start loop). Remove the `gitPort` variable and the `CLOCHE_GIT_REMOTE` env var from the container args. Remove the git daemon tracking at the end of Start(). Remove `cfg.GitRemote` from the env vars passed to the container (line 89).

The Start() method should:
1. Build docker create args (same as today minus `CLOCHE_GIT_REMOTE`)
2. Docker create
3. Copy `cfg.ProjectDir/.` into `/workspace/`
4. Copy Claude auth files
5. Start container
6. Return containerID

**Step 3: Remove cleanup(), FindFreePort(), gitRepoRoot()**

Delete the `cleanup()` method, `FindFreePort()` function, and `gitRepoRoot()` function. Remove the `sync` and `syscall` imports if no longer needed. Remove `net` import too.

**Step 4: Remove cleanup calls from Stop() and Wait()**

In `Stop()`, remove `defer r.cleanup(containerID)`.
In `Wait()`, remove `defer r.cleanup(containerID)`.

**Step 5: Remove unused imports**

Remove `net`, `sync`, `syscall`, `strconv` from the import block (verify each is actually unused first).

**Step 6: Run tests**

Run: `go build ./... && go test ./internal/adapters/docker/...`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/adapters/docker/runtime.go
git commit -m "refactor: remove git daemon from Docker adapter

Result extraction now happens daemon-side via docker cp + git worktrees."
```

---

## Task 3: Remove GitRemote from ContainerConfig and agent push logic

**Files:**
- Modify: `internal/ports/container.go` — remove GitRemote field
- Modify: `internal/agent/runner.go` — remove pushResults and related methods
- Modify: `cmd/cloche-agent/main.go` — remove GitRemote env var handling
- Modify: `internal/adapters/grpc/server.go` — stop passing GitRemote

**Step 1: Remove GitRemote from ContainerConfig**

In `internal/ports/container.go`, remove the `GitRemote` field:

```go
type ContainerConfig struct {
	Image        string
	WorkflowName string
	ProjectDir   string
	NetworkAllow []string
	RunID        string
	Cmd          []string
}
```

**Step 2: Remove GitRemote from RunnerConfig and push logic**

In `internal/agent/runner.go`:
- Remove `GitRemote` field from `RunnerConfig`
- Remove the call to `r.pushResults(ctx, wf.Name)` in `Run()`
- Delete `pushResults()`, `readUserPrompt()`, and `generateCommitMsg()` methods entirely
- Remove unused imports: `bytes`, `log`, `os/exec`

The `RunnerConfig` becomes:

```go
type RunnerConfig struct {
	WorkflowPath string
	WorkDir      string
	StatusOutput io.Writer
	RunID        string
}
```

**Step 3: Update cloche-agent main**

In `cmd/cloche-agent/main.go`, remove the `CLOCHE_GIT_REMOTE` env var reading and unsetting:

```go
runID := os.Getenv("CLOCHE_RUN_ID")
os.Unsetenv("CLOCHE_RUN_ID")

runner := agent.NewRunner(agent.RunnerConfig{
	WorkflowPath: workflowPath,
	WorkDir:      workDir,
	StatusOutput: os.Stdout,
	RunID:        runID,
})
```

**Step 4: Run tests**

Run: `go build ./... && go test ./...`
Expected: PASS (may need to fix tests that reference GitRemote)

**Step 5: Commit**

```bash
git add internal/ports/container.go internal/agent/runner.go cmd/cloche-agent/main.go internal/adapters/grpc/server.go
git commit -m "refactor: remove in-container git push mechanism

GitRemote removed from ContainerConfig and RunnerConfig.
pushResults, readUserPrompt, and generateCommitMsg deleted from agent runner."
```

---

## Task 4: Update Docker adapter to copy from `./cloche/` subdirectory

**Files:**
- Modify: `internal/adapters/docker/runtime.go` — Start() copy path

**Step 1: Change copy-in path**

In the `Start()` method, change the `docker cp` source from `cfg.ProjectDir/.` to `cfg.ProjectDir/cloche/.`:

```go
if cfg.ProjectDir != "" {
	cpCmd := exec.CommandContext(ctx, "docker", "cp", filepath.Join(cfg.ProjectDir, "cloche")+ "/.", containerID+":/workspace/")
	// ... rest unchanged
}
```

Add `"path/filepath"` to the imports if not already present.

**Step 2: Run tests**

Run: `go build ./...`
Expected: Compiles. Integration tests may need updating in later tasks.

**Step 3: Commit**

```bash
git add internal/adapters/docker/runtime.go
git commit -m "feat: copy from ./cloche/ subdirectory into container"
```

---

## Task 5: Add daemon-side result extraction with git worktrees

**Files:**
- Create: `internal/adapters/docker/extract.go`

**Step 1: Create the extraction function**

Create `internal/adapters/docker/extract.go`:

```go
package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ExtractResults copies container workspace to a git branch using worktrees.
// It creates branch cloche/<runID> based on baseSHA, copies the container's
// /workspace into the worktree's cloche/ directory, commits, and cleans up.
func ExtractResults(ctx context.Context, containerID, projectDir, runID, baseSHA, workflowName, result string) error {
	if baseSHA == "" {
		return fmt.Errorf("no base SHA recorded for run %s", runID)
	}

	// 1. Copy container workspace to temp dir
	tmpDir, err := os.MkdirTemp("", "cloche-extract-"+runID+"-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":/workspace/.", tmpDir+"/")
	var cpStderr bytes.Buffer
	cpCmd.Stderr = &cpStderr
	if err := cpCmd.Run(); err != nil {
		return fmt.Errorf("docker cp from container: %s: %w", cpStderr.String(), err)
	}

	// 2. Create git worktree
	branch := "cloche/" + runID
	worktreeDir := filepath.Join(projectDir, ".gitworktrees", "cloche", runID)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", worktreeDir, baseSHA)
	addCmd.Dir = projectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	defer func() {
		// Clean up worktree
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreeDir)
		rmCmd.Dir = projectDir
		rmCmd.Run()
	}()

	// 3. Copy temp dir contents into worktree's cloche/ directory
	clocheInWorktree := filepath.Join(worktreeDir, "cloche")
	if err := os.RemoveAll(clocheInWorktree); err != nil {
		return fmt.Errorf("cleaning worktree cloche dir: %w", err)
	}
	if err := os.MkdirAll(clocheInWorktree, 0755); err != nil {
		return fmt.Errorf("creating worktree cloche dir: %w", err)
	}

	// Use cp -a for recursive copy preserving attributes
	cpLocalCmd := exec.CommandContext(ctx, "cp", "-a", tmpDir+"/.", clocheInWorktree+"/")
	if out, err := cpLocalCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copying to worktree: %s: %w", out, err)
	}

	// 4. Create branch, add, commit
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)

	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	checkoutCmd.Dir = worktreeDir
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	addFilesCmd := exec.CommandContext(ctx, "git", "add", "cloche/")
	addFilesCmd.Dir = worktreeDir
	addFilesCmd.Env = gitEnv
	if out, err := addFilesCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", out, err)
	}

	commitMsg := fmt.Sprintf("cloche run %s: %s (%s)", runID, workflowName, result)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg, "--allow-empty")
	commitCmd.Dir = worktreeDir
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", out, err)
	}

	return nil
}
```

**Step 2: Run tests**

Run: `go build ./...`
Expected: Compiles.

**Step 3: Commit**

```bash
git add internal/adapters/docker/extract.go
git commit -m "feat: add daemon-side result extraction with git worktrees"
```

---

## Task 6: Wire extraction into gRPC server's trackRun

**Files:**
- Modify: `internal/adapters/grpc/server.go` — launchAndTrack and trackRun

**Step 1: Record baseSHA in launchAndTrack**

In `launchAndTrack()`, before calling `s.container.Start()`, capture the current HEAD:

```go
func (s *ClocheServer) launchAndTrack(runID, image string, keepContainer bool, req *pb.RunWorkflowRequest) {
	ctx := context.Background()

	// Record base SHA for result branch creation
	baseSHA := gitHEAD(req.ProjectDir)

	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		// ... same as before but without GitRemote
	})
	// ...

	run, _ := s.store.GetRun(ctx, runID)
	if run != nil {
		run.Start()
		run.ContainerID = containerID
		run.BaseSHA = baseSHA
		_ = s.store.UpdateRun(ctx, run)
	}

	s.trackRun(runID, containerID, req.ProjectDir, req.WorkflowName, keepContainer)
}
```

Add helper function:

```go
func gitHEAD(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Add `"os/exec"` to imports.

**Step 2: Call ExtractResults in trackRun**

In `trackRun()`, after extracting step output files and before marking run complete, add the extraction call:

```go
// Extract results to git branch via worktree
run2, _ := s.store.GetRun(ctx, runID)
if run2 != nil && run2.BaseSHA != "" {
	resultState := "completed"
	if run2.State == domain.RunStateSucceeded {
		resultState = "succeeded"
	} else if run2.State == domain.RunStateFailed {
		resultState = "failed"
	}
	if err := docker.ExtractResults(ctx, containerID, run2.ProjectDir, runID, run2.BaseSHA, workflowName, resultState); err != nil {
		log.Printf("run %s: failed to extract results to branch: %v", runID, err)
	}
}
```

Add import for `"github.com/cloche-dev/cloche/internal/adapters/docker"`.

**Step 3: Update prompt path in RunWorkflow**

The prompt is now written into the `cloche/` subdirectory:

```go
if req.Prompt != "" {
	clocheDir := filepath.Join(req.ProjectDir, "cloche", ".cloche", runID)
	// ... rest same
}
```

**Step 4: Update output extraction path in trackRun**

The output paths need to reference the `cloche/` subdirectory:

```go
outputDst := filepath.Join(projectDir, "cloche", ".cloche", runID, "output")
```

And the log reading in StreamLogs:

```go
outputPath := filepath.Join(run.ProjectDir, "cloche", ".cloche", req.RunId, "output", exec.StepName+".log")
// ...
containerLogPath := filepath.Join(run.ProjectDir, "cloche", ".cloche", req.RunId, "output", "container.log")
```

**Step 5: Run tests**

Run: `go build ./... && go test ./internal/adapters/grpc/...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/adapters/grpc/server.go
git commit -m "feat: wire daemon-side extraction into trackRun

Records baseSHA at run start, calls ExtractResults after container
completion to create cloche/<run-id> branch via git worktree."
```

---

## Task 7: Update `cloche init` for subdirectory layout

**Files:**
- Modify: `cmd/cloche/init.go`

**Step 1: Rewrite cmdInit to scaffold into ./cloche/**

```go
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

	clocheDir := "cloche"
	workflowFile := filepath.Join(clocheDir, workflow+".cloche")

	// Refuse to overwrite existing workflow
	if _, err := os.Stat(workflowFile); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists\n", workflowFile)
		os.Exit(1)
	}

	// Create directories
	dirs := []string{
		clocheDir,
		filepath.Join(clocheDir, ".cloche", "prompts"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating %s: %v\n", d, err)
			os.Exit(1)
		}
	}

	// Write all files, skipping any that already exist
	files := []struct{ path, content string }{
		{workflowFile, fmt.Sprintf(workflowTemplate, workflow)},
		{filepath.Join(clocheDir, "Dockerfile"), fmt.Sprintf(dockerfileTemplate, image)},
		{filepath.Join(clocheDir, ".cloche", "prompts", "implement.md"), implementPrompt},
		{filepath.Join(clocheDir, ".cloche", "prompts", "fix.md"), fixPrompt},
	}

	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			fmt.Fprintf(os.Stderr, "  skip %s (already exists)\n", f.path)
			continue
		}
		if err := os.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f.path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  create %s\n", f.path)
	}

	// Add gitignore entries
	addGitignoreEntries([]string{"cloche/.cloche/*/", ".gitworktrees/"})

	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Edit %s — change the test command for your project\n", workflowFile)
	fmt.Fprintf(os.Stderr, "  2. Edit %s — add your project's dependencies\n", filepath.Join(clocheDir, "Dockerfile"))
	fmt.Fprintf(os.Stderr, "  3. docker build -t cloche-agent %s\n", clocheDir)
	fmt.Fprintf(os.Stderr, "  4. cloche run --workflow %s --prompt \"...\"\n", workflow)
}

func addGitignoreEntries(entries []string) {
	gitignorePath := ".gitignore"
	existing, _ := os.ReadFile(gitignorePath)
	content := string(existing)

	var toAdd []string
	for _, entry := range entries {
		if !strings.Contains(content, entry) {
			toAdd = append(toAdd, entry)
		}
	}

	if len(toAdd) == 0 {
		return
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	if len(existing) > 0 && !strings.HasSuffix(content, "\n") {
		f.WriteString("\n")
	}
	f.WriteString("\n# Cloche\n")
	for _, entry := range toAdd {
		f.WriteString(entry + "\n")
	}
}
```

Add `"strings"` to imports.

**Step 2: Update workflow template prompt paths**

The workflow template's `file()` references need to point to `.cloche/prompts/`:

```go
var workflowTemplate = `workflow "%s" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file(".cloche/prompts/fix.md")
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
```

**Step 3: Run tests**

Run: `go build ./...`
Expected: Compiles.

**Step 4: Commit**

```bash
git add cmd/cloche/init.go
git commit -m "feat: update cloche init to scaffold into ./cloche/ subdirectory"
```

---

## Task 8: Update `cloche run` to resolve paths from ./cloche/

**Files:**
- Modify: `cmd/cloche/main.go` — cmdRun function

**Step 1: Update workflow file path resolution**

In `cmdRun()`, look for the workflow file inside `./cloche/`:

```go
cwd, _ := os.Getwd()

// Resolve image from workflow file in cloche/ subdirectory
var image string
wfPath := filepath.Join(cwd, "cloche", workflow+".cloche")
if data, err := os.ReadFile(wfPath); err == nil {
	if wf, err := dsl.Parse(string(data)); err == nil {
		image = wf.Config["container.image"]
	}
}
```

The `ProjectDir` sent to the daemon remains `cwd` (the host project root). The daemon knows to look inside `./cloche/`.

**Step 2: Run tests**

Run: `go build ./...`
Expected: Compiles.

**Step 3: Commit**

```bash
git add cmd/cloche/main.go
git commit -m "feat: resolve workflow files from ./cloche/ subdirectory"
```

---

## Task 9: Add container_alive and container_dead_since to proto

**Files:**
- Modify: `api/proto/cloche/v1/cloche.proto`
- Regenerate: `api/clochepb/` (generated code)

**Step 1: Update GetStatusResponse in proto**

Add two fields to `GetStatusResponse`:

```protobuf
message GetStatusResponse {
  string run_id = 1;
  string workflow_name = 2;
  string state = 3;
  string current_step = 4;
  repeated StepExecutionStatus step_executions = 5;
  string error_message = 6;
  string container_id = 7;
  bool container_alive = 8;
  string container_dead_since = 9;
}
```

Using `string` for `container_dead_since` to stay consistent with the existing timestamp pattern (all other timestamps are strings in this proto).

**Step 2: Regenerate gRPC code**

Run: `protoc --go_out=. --go-grpc_out=. api/proto/cloche/v1/cloche.proto`

Or if the project uses `buf`: `buf generate`

Check what generation tool is used by looking at existing go_package option and Makefile/scripts.

**Step 3: Commit**

```bash
git add api/proto/cloche/v1/cloche.proto api/clochepb/
git commit -m "feat: add container_alive and container_dead_since to GetStatusResponse"
```

---

## Task 10: Implement container liveness check in GetStatus handler

**Files:**
- Modify: `internal/adapters/grpc/server.go` — GetStatus handler
- Modify: `internal/adapters/docker/runtime.go` — add Inspect method
- Modify: `internal/ports/container.go` — add Inspect to interface

**Step 1: Add Inspect method to ContainerRuntime interface**

In `internal/ports/container.go`:

```go
type ContainerStatus struct {
	Running  bool
	ExitCode int
	FinishedAt time.Time
}

type ContainerRuntime interface {
	Start(ctx context.Context, cfg ContainerConfig) (containerID string, err error)
	Stop(ctx context.Context, containerID string) error
	AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error)
	Wait(ctx context.Context, containerID string) (exitCode int, err error)
	CopyFrom(ctx context.Context, containerID string, srcPath, dstPath string) error
	Logs(ctx context.Context, containerID string) (string, error)
	Remove(ctx context.Context, containerID string) error
	Inspect(ctx context.Context, containerID string) (*ContainerStatus, error)
}
```

Add `"time"` to imports.

**Step 2: Implement Inspect in Docker adapter**

In `internal/adapters/docker/runtime.go`:

```go
func (r *Runtime) Inspect(ctx context.Context, containerID string) (*ports.ContainerStatus, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.State.Running}} {{.State.ExitCode}} {{.State.FinishedAt}}",
		containerID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("inspecting container: %s: %w", stderr.String(), err)
	}

	parts := strings.SplitN(strings.TrimSpace(stdout.String()), " ", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected inspect output: %s", stdout.String())
	}

	running := parts[0] == "true"
	exitCode := 0
	fmt.Sscanf(parts[1], "%d", &exitCode)
	finishedAt, _ := time.Parse(time.RFC3339Nano, parts[2])

	return &ports.ContainerStatus{
		Running:    running,
		ExitCode:   exitCode,
		FinishedAt: finishedAt,
	}, nil
}
```

Add `"time"` to imports.

**Step 3: Implement Inspect in local adapter (for tests)**

In `internal/adapters/local/runtime.go`, add a stub:

```go
func (r *Runtime) Inspect(ctx context.Context, containerID string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{Running: false}, nil
}
```

**Step 4: Update GetStatus handler to include container liveness**

In `internal/adapters/grpc/server.go`, update `GetStatus()`:

```go
func (s *ClocheServer) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}

	resp := &pb.GetStatusResponse{
		RunId:        run.ID,
		WorkflowName: run.WorkflowName,
		State:        string(run.State),
		CurrentStep:  strings.Join(run.ActiveSteps, ","),
		ErrorMessage: run.ErrorMessage,
		ContainerId:  run.ContainerID,
	}

	// Check container liveness if we have a container and run is active
	if run.ContainerID != "" && s.container != nil {
		if status, err := s.container.Inspect(ctx, run.ContainerID); err == nil {
			resp.ContainerAlive = status.Running
			if !status.Running && !status.FinishedAt.IsZero() {
				resp.ContainerDeadSince = status.FinishedAt.Format(time.RFC3339Nano)
			}
		}
	}

	// Load step executions (unchanged)
	// ...

	return resp, nil
}
```

**Step 5: Run tests**

Run: `go build ./... && go test ./internal/adapters/grpc/... ./internal/adapters/docker/...`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/ports/container.go internal/adapters/docker/runtime.go internal/adapters/local/runtime.go internal/adapters/grpc/server.go
git commit -m "feat: add container liveness check to GetStatus

Adds Inspect method to ContainerRuntime interface.
GetStatus now reports container_alive and container_dead_since."
```

---

## Task 11: Add `poll` CLI command

**Files:**
- Create: `cmd/cloche/poll.go`
- Modify: `cmd/cloche/main.go` — register command

**Step 1: Create poll.go**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

func cmdPoll(client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche poll <run-id>\n")
		os.Exit(1)
	}

	runID := args[0]
	pollInterval := 2 * time.Second
	containerDeadThreshold := 1 * time.Minute

	// Track what we've already printed to avoid duplicates
	var lastStepCount int
	var lastState string

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{RunId: runID})
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Print new step events
		for i := lastStepCount; i < len(resp.StepExecutions); i++ {
			exec := resp.StepExecutions[i]
			ts := time.Now().Format("15:04:05")
			if exec.Result == "" {
				fmt.Printf("[%s] Step %q started\n", ts, exec.StepName)
			} else {
				fmt.Printf("[%s] Step %q completed: %s\n", ts, exec.StepName, exec.Result)
			}
		}
		lastStepCount = len(resp.StepExecutions)

		// Print state changes
		if resp.State != lastState {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("[%s] Run %s is %s\n", ts, runID, resp.State)
			lastState = resp.State
		}

		// Check terminal states
		switch resp.State {
		case "succeeded":
			os.Exit(0)
		case "failed", "cancelled":
			if resp.ErrorMessage != "" {
				fmt.Fprintf(os.Stderr, "Error: %s\n", resp.ErrorMessage)
			}
			os.Exit(1)
		}

		// Check container death
		if !resp.ContainerAlive && resp.ContainerDeadSince != "" {
			deadSince, err := time.Parse(time.RFC3339Nano, resp.ContainerDeadSince)
			if err == nil && time.Since(deadSince) > containerDeadThreshold {
				fmt.Fprintf(os.Stderr, "[%s] Container has been dead for >1 minute (since %s)\n",
					time.Now().Format("15:04:05"), deadSince.Format("15:04:05"))
				os.Exit(1)
			}
		}

		time.Sleep(pollInterval)
	}
}
```

**Step 2: Register poll command in main.go**

In `cmd/cloche/main.go`, add the `poll` case to the switch and update usage:

In the command switch (around line 49):
```go
case "poll":
	cmdPoll(client, os.Args[2:])
```

In the usage string:
```
  poll <run-id>                              Wait for a run to finish
```

**Step 3: Run tests**

Run: `go build ./...`
Expected: Compiles.

**Step 4: Commit**

```bash
git add cmd/cloche/poll.go cmd/cloche/main.go
git commit -m "feat: add poll command to wait for run completion

Streams status changes, exits 0 on success, 1 on failure.
Detects dead containers (>1 min) and exits with error."
```

---

## Task 12: Update integration smoke test

**Files:**
- Modify: `test/integration/smoke_test.go`

**Step 1: Read existing smoke test**

Read the file and understand the current test setup, then update it to:
- Create workflow files under a `cloche/` subdirectory in the temp dir
- Verify the agent still executes the workflow correctly from the new layout

**Step 2: Run integration tests**

Run: `go test ./test/integration/... -v -count=1`
Expected: PASS

**Step 3: Commit**

```bash
git add test/integration/smoke_test.go
git commit -m "test: update smoke test for ./cloche/ subdirectory layout"
```

---

## Task 13: Final verification

**Step 1: Full build**

Run: `go build ./...`
Expected: All three binaries compile.

**Step 2: Full test suite**

Run: `go test ./... -count=1`
Expected: All tests pass.

**Step 3: Verify go vet and any linters**

Run: `go vet ./...`
Expected: No issues.

**Step 4: Final commit if any cleanup needed**

```bash
git add -A
git commit -m "chore: final cleanup for subdirectory layout migration"
```
