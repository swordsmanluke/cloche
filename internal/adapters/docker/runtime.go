package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/cloche-dev/cloche/internal/ports"
)

type Runtime struct{}

func NewRuntime() (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found in PATH: %w", err)
	}
	return &Runtime{}, nil
}

func (r *Runtime) Start(ctx context.Context, cfg ports.ContainerConfig) (string, error) {
	args := []string{
		"run", "-d",
		"--workdir", "/workspace",
	}

	// Bind-mount project directory into container
	if cfg.ProjectDir != "" {
		args = append(args, "-v", cfg.ProjectDir+":/workspace")
	}

	// Pass ANTHROPIC_API_KEY into container if set
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		args = append(args, "-e", "ANTHROPIC_API_KEY")
	}

	// Mount ~/.claude config directory for CLI auth (OAuth session reuse)
	// Container runs as "agent" user with home /home/agent
	if home, err := os.UserHomeDir(); err == nil {
		claudeDir := home + "/.claude"
		if _, err := os.Stat(claudeDir); err == nil {
			args = append(args, "-v", claudeDir+":/home/agent/.claude")
		}
		claudeJSON := home + "/.claude.json"
		if _, err := os.Stat(claudeJSON); err == nil {
			args = append(args, "-v", claudeJSON+":/home/agent/.claude.json:ro")
		}
	}

	if len(cfg.NetworkAllow) == 0 {
		args = append(args, "--network", "none")
	}

	containerCmd := cfg.Cmd
	if len(containerCmd) == 0 {
		containerCmd = []string{"cloche-agent", cfg.WorkflowName + ".cloche"}
	}

	args = append(args, cfg.Image)
	args = append(args, containerCmd...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("starting container: %s: %w", stderr.String(), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (r *Runtime) Stop(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stopping container: %s: %w", stderr.String(), err)
	}
	return nil
}

func (r *Runtime) AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", containerID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("attaching to container output: %w", err)
	}

	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

func (r *Runtime) Wait(ctx context.Context, containerID string) (int, error) {
	cmd := exec.CommandContext(ctx, "docker", "wait", containerID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return -1, fmt.Errorf("waiting for container: %s: %w", stderr.String(), err)
	}

	code, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil {
		return -1, fmt.Errorf("parsing exit code %q: %w", stdout.String(), err)
	}
	return code, nil
}

type cmdReadCloser struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cmd.Wait()
	return err
}
