package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/cloche-dev/cloche/internal/ports"
)

type Runtime struct {
	mu         sync.Mutex
	gitDaemons map[string]*exec.Cmd // containerID -> git daemon process
}

func NewRuntime() (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found in PATH: %w", err)
	}
	return &Runtime{
		gitDaemons: make(map[string]*exec.Cmd),
	}, nil
}

func (r *Runtime) Start(ctx context.Context, cfg ports.ContainerConfig) (string, error) {
	// 1. Find the git repo root containing the project dir
	repoRoot, err := gitRepoRoot(cfg.ProjectDir)
	if err != nil {
		return "", fmt.Errorf("finding git repo root: %w", err)
	}

	// Start git daemon to receive pushes from the container.
	// Use OS-assigned free port with retry to avoid collisions.
	var gitPort int
	var gitCmd *exec.Cmd
	for attempt := 0; attempt < 5; attempt++ {
		gitPort, err = FindFreePort()
		if err != nil {
			return "", fmt.Errorf("finding free port: %w", err)
		}
		gitCmd = exec.Command("git", "daemon",
			"--reuseaddr",
			"--port="+strconv.Itoa(gitPort),
			"--base-path="+repoRoot,
			"--export-all",
			"--enable=receive-pack",
			repoRoot,
		)
		gitCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := gitCmd.Start(); err == nil {
			break
		}
		if attempt == 4 {
			return "", fmt.Errorf("starting git daemon after 5 attempts: %w", err)
		}
	}

	// 2. Build docker create args
	containerCmd := cfg.Cmd
	useDefaultCmd := len(containerCmd) == 0
	if useDefaultCmd {
		containerCmd = []string{"cloche-agent", cfg.WorkflowName + ".cloche"}
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

	// Pass run ID and git remote into container
	if cfg.RunID != "" {
		args = append(args, "-e", "CLOCHE_RUN_ID="+cfg.RunID)
	}
	args = append(args, "-e", fmt.Sprintf("CLOCHE_GIT_REMOTE=git://host.docker.internal:%d/", gitPort))

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
		// Wrap: chown workspace to agent, then exec as agent user
		wrappedCmd := fmt.Sprintf(
			"chown -R agent:agent /workspace && exec su agent -s /bin/sh -c %q",
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
		syscall.Kill(-gitCmd.Process.Pid, syscall.SIGKILL)
		gitCmd.Wait()
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
			gitCmd.Process.Kill()
			gitCmd.Wait()
			return "", fmt.Errorf("copying files to container: %s: %w", cpStderr.String(), err)
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
		syscall.Kill(-gitCmd.Process.Pid, syscall.SIGKILL)
		gitCmd.Wait()
		return "", fmt.Errorf("starting container: %s: %w", startStderr.String(), err)
	}

	// 5. Track git daemon for cleanup
	r.mu.Lock()
	r.gitDaemons[containerID] = gitCmd
	r.mu.Unlock()

	return containerID, nil
}

func (r *Runtime) Stop(ctx context.Context, containerID string) error {
	defer r.cleanup(containerID)

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
	defer r.cleanup(containerID)

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

// FindFreePort asks the OS for an available TCP port.
func FindFreePort() (int, error) {
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()
	return port, nil
}

func gitRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Runtime) cleanup(containerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd, ok := r.gitDaemons[containerID]; ok {
		// Kill the entire process group (git daemon forks child processes)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		delete(r.gitDaemons, containerID)
	}
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
