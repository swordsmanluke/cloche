package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	// Build docker create args
	containerCmd := cfg.Cmd
	useDefaultCmd := len(containerCmd) == 0
	if useDefaultCmd {
		containerCmd = []string{"cloche-agent", ".cloche/" + cfg.WorkflowName + ".cloche"}
	}

	args := []string{
		"create",
		"--workdir", "/workspace",
		"--add-host=host.docker.internal:host-gateway",
	}

	// When using the default command, run as root and wrap with chown + su agent
	// so the workspace is owned by the agent user. Custom Cmd runs as-is.
	if useDefaultCmd {
		args = append(args, "--user", "root")
	}

	// Pass run ID into container
	if cfg.RunID != "" {
		args = append(args, "-e", "CLOCHE_RUN_ID="+cfg.RunID)
	}
	// Pass ANTHROPIC_API_KEY into container if set
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		args = append(args, "-e", "ANTHROPIC_API_KEY")
	}

	// Claude auth files are copied (not mounted) after docker create so each
	// container gets its own copy — avoids concurrent write conflicts.

	// Support extra volume mounts via CLOCHE_EXTRA_MOUNTS (comma-separated host:container pairs)
	if mounts := os.Getenv("CLOCHE_EXTRA_MOUNTS"); mounts != "" {
		for _, m := range strings.Split(mounts, ",") {
			if strings.Contains(m, ":") {
				args = append(args, "-v", m)
			}
		}
	}

	// Support extra env vars via CLOCHE_EXTRA_ENV (comma-separated KEY=VALUE pairs)
	if extraEnv := os.Getenv("CLOCHE_EXTRA_ENV"); extraEnv != "" {
		for _, e := range strings.Split(extraEnv, ",") {
			if strings.Contains(e, "=") {
				args = append(args, "-e", e)
			}
		}
	}

	// No --network none: agent needs network for git push and API access

	if useDefaultCmd {
		// Wrap: chown workspace, strip host-side tools (e.g. Serena MCP) that
		// cause the agent to waste time on onboarding, then exec as agent.
		wrappedCmd := fmt.Sprintf(
			"chown -R agent:agent /workspace"+
				" && chown -R agent:agent /home/agent/.claude /home/agent/.claude.json 2>/dev/null"+
				" && rm -rf /workspace/.serena"+
				" && f=/home/agent/.claude/settings.json"+
				` && [ -f "$f" ] && sed -i '/"enabledPlugins"/,/}/d' "$f"`+
				"; exec su agent -s /bin/sh -c %q",
			strings.Join(containerCmd, " "),
		)
		args = append(args, cfg.Image, "sh", "-c", wrappedCmd)
	} else {
		args = append(args, cfg.Image)
		args = append(args, containerCmd...)
	}

	// docker create
	createCmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	createCmd.Stdout = &stdout
	createCmd.Stderr = &stderr
	if err := createCmd.Run(); err != nil {
		return "", fmt.Errorf("creating container: %s: %w", stderr.String(), err)
	}
	containerID := strings.TrimSpace(stdout.String())

	// 3. Copy project files into container (no bind mount)
	if cfg.ProjectDir != "" {
		cpCmd := exec.CommandContext(ctx, "docker", "cp", cfg.ProjectDir+"/.", containerID+":/workspace/")
		var cpStderr bytes.Buffer
		cpCmd.Stderr = &cpStderr
		if err := cpCmd.Run(); err != nil {
			// Cleanup on failure
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("copying files to container: %s: %w", cpStderr.String(), err)
		}

		// Apply override files from .cloche/overrides/ on top of workspace
		overridesDir := filepath.Join(cfg.ProjectDir, ".cloche", "overrides")
		if _, err := os.Stat(overridesDir); err == nil {
			overrideCmd := exec.CommandContext(ctx, "docker", "cp", overridesDir+"/.", containerID+":/workspace/")
			overrideCmd.Stderr = &cpStderr
			if err := overrideCmd.Run(); err != nil {
				// Non-fatal: log but don't fail the run
				fmt.Fprintf(os.Stderr, "warning: copying overrides: %s\n", cpStderr.String())
			}
		}
	}

	// 4. Copy Claude auth files into container (each gets its own copy)
	if home, err := os.UserHomeDir(); err == nil {
		claudeDir := home + "/.claude"
		if _, err := os.Stat(claudeDir); err == nil {
			exec.CommandContext(ctx, "docker", "cp", claudeDir, containerID+":/home/agent/.claude").Run()
		}
		claudeJSON := home + "/.claude.json"
		if _, err := os.Stat(claudeJSON); err == nil {
			exec.CommandContext(ctx, "docker", "cp", claudeJSON, containerID+":/home/agent/.claude.json").Run()
		}
	}

	// 5. Start the container
	startCmd := exec.CommandContext(ctx, "docker", "start", containerID)
	var startStderr bytes.Buffer
	startCmd.Stderr = &startStderr
	if err := startCmd.Run(); err != nil {
		exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
		return "", fmt.Errorf("starting container: %s: %w", startStderr.String(), err)
	}

	return containerID, nil
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

func (r *Runtime) CopyFrom(ctx context.Context, containerID string, srcPath, dstPath string) error {
	cmd := exec.CommandContext(ctx, "docker", "cp", containerID+":"+srcPath, dstPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying from container: %s: %w", stderr.String(), err)
	}
	return nil
}

func (r *Runtime) Logs(ctx context.Context, containerID string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", containerID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("getting container logs: %s: %w", stderr.String(), err)
	}
	// Combine stdout and stderr
	combined := stdout.String() + stderr.String()
	return combined, nil
}

func (r *Runtime) Remove(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing container: %s: %w", stderr.String(), err)
	}
	return nil
}

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

func (r *Runtime) Commit(ctx context.Context, containerID string) (string, error) {
	short := containerID
	if len(short) > 12 {
		short = short[:12]
	}
	tag := "cloche-resume-" + short
	cmd := exec.CommandContext(ctx, "docker", "commit", containerID, tag)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("committing container: %s: %w", stderr.String(), err)
	}
	return tag, nil
}

func (r *Runtime) CopyTo(ctx context.Context, containerID string, srcPath, dstPath string) error {
	cmd := exec.CommandContext(ctx, "docker", "cp", srcPath, containerID+":"+dstPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying to container: %s: %w", stderr.String(), err)
	}
	return nil
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