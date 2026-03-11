package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloche-dev/cloche/internal/ports"
)

type managedProcess struct {
	cmd        *exec.Cmd
	stdout     io.ReadCloser
	done       chan struct{}
	exit       int
	projectDir string
}

type Runtime struct {
	agentBinary string
	mu          sync.Mutex
	processes   map[string]*managedProcess
	nextID      int
}

func NewRuntime(agentBinary string) *Runtime {
	return &Runtime{
		agentBinary: agentBinary,
		processes:   make(map[string]*managedProcess),
	}
}

func (r *Runtime) Start(ctx context.Context, cfg ports.ContainerConfig) (string, error) {
	// Resolve workflow file path
	workflowPath := filepath.Join(cfg.ProjectDir, ".cloche", cfg.WorkflowName+".cloche")

	agentCmd := cfg.Cmd
	if len(agentCmd) == 0 {
		agentCmd = []string{r.agentBinary, workflowPath}
	}

	cmd := exec.CommandContext(ctx, agentCmd[0], agentCmd[1:]...)
	cmd.Dir = cfg.ProjectDir
	cmd.Env = append(os.Environ(), "CLOCHE_RUN_ID="+cfg.RunID)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting agent process: %w", err)
	}

	r.mu.Lock()
	r.nextID++
	id := fmt.Sprintf("local-%d", r.nextID)
	mp := &managedProcess{
		cmd:        cmd,
		stdout:     stdout,
		done:       make(chan struct{}),
		projectDir: cfg.ProjectDir,
	}
	r.processes[id] = mp
	r.mu.Unlock()

	return id, nil
}

func (r *Runtime) Stop(ctx context.Context, containerID string) error {
	r.mu.Lock()
	mp, ok := r.processes[containerID]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("process %q not found", containerID)
	}

	if mp.cmd.Process != nil {
		return mp.cmd.Process.Kill()
	}
	return nil
}

func (r *Runtime) AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error) {
	r.mu.Lock()
	mp, ok := r.processes[containerID]
	r.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("process %q not found", containerID)
	}

	return mp.stdout, nil
}

func (r *Runtime) Wait(ctx context.Context, containerID string) (int, error) {
	r.mu.Lock()
	mp, ok := r.processes[containerID]
	r.mu.Unlock()

	if !ok {
		return -1, fmt.Errorf("process %q not found", containerID)
	}

	// Call cmd.Wait() in a goroutine so we can respect context cancellation.
	// cmd.Wait() must be called AFTER all reads from StdoutPipe are complete,
	// which is guaranteed since trackRun drains the pipe before calling Wait().
	type waitResult struct {
		exit int
		err  error
	}
	ch := make(chan waitResult, 1)
	go func() {
		err := mp.cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				ch <- waitResult{exit: exitErr.ExitCode()}
			} else {
				ch <- waitResult{exit: -1, err: err}
			}
		} else {
			ch <- waitResult{exit: 0}
		}
		close(mp.done)
	}()

	select {
	case res := <-ch:
		return res.exit, res.err
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (r *Runtime) Logs(ctx context.Context, containerID string) (string, error) {
	return "", nil
}

func (r *Runtime) Remove(ctx context.Context, containerID string) error {
	return nil
}

func (r *Runtime) Inspect(ctx context.Context, containerID string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{Running: false}, nil
}

func (r *Runtime) CopyFrom(ctx context.Context, containerID string, srcPath, dstPath string) error {
	r.mu.Lock()
	mp, ok := r.processes[containerID]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("process %q not found", containerID)
	}

	// In local mode the workspace IS the project dir. Container-internal
	// absolute paths (e.g. /workspace/.cloche/output/) are mapped to the
	// project directory, matching what Docker does via volume mounts.
	src := srcPath
	if strings.HasPrefix(srcPath, "/workspace/") || srcPath == "/workspace" {
		src = filepath.Join(mp.projectDir, strings.TrimPrefix(srcPath, "/workspace"))
	} else if !filepath.IsAbs(srcPath) {
		src = filepath.Join(mp.projectDir, srcPath)
	}

	if err := os.MkdirAll(dstPath, 0o755); err != nil {
		return fmt.Errorf("creating destination dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "cp", "-r", src+"/.", dstPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copying files: %s: %w", string(out), err)
	}
	return nil
}
