package local

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/cloche-dev/cloche/internal/ports"
)

type managedProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	done   chan struct{}
	exit   int
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
	workflowPath := filepath.Join(cfg.ProjectDir, cfg.WorkflowName+".cloche")

	agentCmd := cfg.Cmd
	if len(agentCmd) == 0 {
		agentCmd = []string{r.agentBinary, workflowPath}
	}

	cmd := exec.CommandContext(ctx, agentCmd[0], agentCmd[1:]...)
	cmd.Dir = cfg.ProjectDir

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
		cmd:    cmd,
		stdout: stdout,
		done:   make(chan struct{}),
	}
	r.processes[id] = mp
	r.mu.Unlock()

	// Background goroutine to track process exit
	go func() {
		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				mp.exit = exitErr.ExitCode()
			} else {
				mp.exit = -1
			}
		}
		close(mp.done)
	}()

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

	select {
	case <-mp.done:
		return mp.exit, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
