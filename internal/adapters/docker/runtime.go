package docker

import (
	"archive/tar"
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
		"--dns", "8.8.8.8",
		"--dns", "8.8.4.4",
		"--log-driver", "json-file",
	}

	if cfg.Interactive {
		args = append(args, "-i", "-t")
	}

	// Name container uniquely. Use task-attempt-workflow when available
	// (allows concurrent runs of the same workflow), fall back to run ID.
	containerName := cfg.RunID
	if cfg.TaskID != "" && cfg.AttemptID != "" {
		containerName = cfg.AttemptID + "-" + cfg.WorkflowName
	}
	if containerName != "" {
		args = append(args, "--name", containerName)
	}

	// Pass run ID, task ID, and attempt ID into container
	if cfg.RunID != "" {
		args = append(args, "-e", "CLOCHE_RUN_ID="+cfg.RunID)
	}
	if cfg.TaskID != "" {
		args = append(args, "-e", "CLOCHE_TASK_ID="+cfg.TaskID)
	}
	if cfg.AttemptID != "" {
		args = append(args, "-e", "CLOCHE_ATTEMPT_ID="+cfg.AttemptID)
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
		// Start as root to fix ownership of docker-cp'd files, then drop
		// to the agent user via gosu (direct setuid+exec, no intermediate
		// shell — avoids stdout buffering issues that su/sh cause).
		args = append(args, "--user", "root")
		wrappedCmd := fmt.Sprintf(
			"chown -R agent:agent /workspace"+
				" && chown -R agent:agent /home/agent/.claude 2>/dev/null"+
				" && rm -rf /workspace/.serena"+
				" && f=/home/agent/.claude/settings.json"+
				` && [ -f "$f" ] && sed -i '/"enabledPlugins"/,/}/d' "$f"`+
				"; exec gosu agent %s",
			strings.Join(containerCmd, " "),
		)
		args = append(args, cfg.Image, "sh", "-c", wrappedCmd)
	} else if cfg.Interactive {
		// Interactive console: same chown + gosu wrapper so auth files
		// and project files are accessible to the agent user.
		args = append(args, "--user", "root")
		wrappedCmd := fmt.Sprintf(
			"chown -R agent:agent /workspace"+
				" && chown -R agent:agent /home/agent/.claude 2>/dev/null"+
				" && chown agent:agent /home/agent/.claude.json 2>/dev/null"+
				" && f=/home/agent/.claude/settings.json"+
				` && [ -f "$f" ] && sed -i '/"enabledPlugins"/,/}/d' "$f"`+
				"; exec gosu agent %s",
			strings.Join(containerCmd, " "),
		)
		args = append(args, cfg.Image, "sh", "-c", wrappedCmd)
	} else {
		// Custom command on a non-cloche image (e.g. tests with alpine).
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

	// 3. Copy project files into container, respecting .clocheignore
	if cfg.ProjectDir != "" {
		patterns, err := parseClocheignore(cfg.ProjectDir)
		if err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("parsing .clocheignore: %w", err)
		}

		if err := copyProjectToContainer(ctx, cfg.ProjectDir, containerID, patterns); err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", err
		}

		// Apply override files from .cloche/overrides/ on top of workspace
		overridesDir := filepath.Join(cfg.ProjectDir, ".cloche", "overrides")
		if _, err := os.Stat(overridesDir); err == nil {
			overrideCmd := exec.CommandContext(ctx, "docker", "cp", overridesDir+"/.", containerID+":/workspace/")
			var cpStderr bytes.Buffer
			overrideCmd.Stderr = &cpStderr
			if err := overrideCmd.Run(); err != nil {
				// Non-fatal: log but don't fail the run
				fmt.Fprintf(os.Stderr, "warning: copying overrides: %s\n", cpStderr.String())
			}
		}
	}

	// 3b. Write prompt into container (.cloche/runs/ is excluded by .clocheignore,
	//      so prompt.txt must be injected separately).
	if cfg.Prompt != "" && cfg.TaskID != "" {
		promptDir := filepath.Join(os.TempDir(), "cloche-prompt-"+cfg.RunID)
		runsDir := filepath.Join(promptDir, ".cloche", "runs", cfg.TaskID)
		if err := os.MkdirAll(runsDir, 0755); err == nil {
			_ = os.WriteFile(filepath.Join(runsDir, "prompt.txt"), []byte(cfg.Prompt), 0644)
			cpCmd := exec.CommandContext(ctx, "docker", "cp", promptDir+"/.", containerID+":/workspace/")
			_ = cpCmd.Run()
			os.RemoveAll(promptDir)
		}
	}

	// 4. Copy Claude auth files into container (each gets its own copy).
	// Only copy auth-relevant files — not the full ~/.claude directory
	// which contains large history, session, and debug data.
	if home, err := os.UserHomeDir(); err == nil {
		claudeDir := home + "/.claude"
		// Stage auth files in a temp directory, then docker cp the
		// directory into the container. This avoids needing `docker exec`
		// (container isn't running yet) and ensures the directory exists.
		tmpAuth, tmpErr := os.MkdirTemp("", "cloche-auth")
		if tmpErr == nil {
			defer os.RemoveAll(tmpAuth)
			for _, name := range []string{".credentials.json", "settings.json", "settings.local.json"} {
				src := filepath.Join(claudeDir, name)
				if data, err := os.ReadFile(src); err == nil {
					os.WriteFile(filepath.Join(tmpAuth, name), data, 0644)
				}
			}
			exec.CommandContext(ctx, "docker", "cp", tmpAuth+"/.", containerID+":/home/agent/.claude/").Run()
		}
		// ~/.claude.json contains UI state needed by interactive Claude Code
		// sessions (e.g. "already set up" flag). Copy it for interactive
		// containers; skip for autonomous runs where it causes cached
		// rate-limit info to leak between containers.
		if cfg.Interactive {
			claudeJSON := home + "/.claude.json"
			if _, statErr := os.Stat(claudeJSON); statErr == nil {
				exec.CommandContext(ctx, "docker", "cp", claudeJSON, containerID+":/home/agent/.claude.json").Run()
			}
		}
	}

	// 5. Start the container (skip for interactive — Attach handles start).
	if !cfg.Interactive {
		startCmd := exec.CommandContext(ctx, "docker", "start", containerID)
		var startStderr bytes.Buffer
		startCmd.Stderr = &startStderr
		if err := startCmd.Run(); err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("starting container: %s: %w", startStderr.String(), err)
		}
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
	// Merge container stderr into the same pipe so we capture all output.
	cmd.Stderr = cmd.Stdout

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

// Attach connects to a running interactive container's stdin/stdout/stderr.
// The container must have been started with Interactive=true. Returns a
// ReadWriteCloser that forwards writes to the container's stdin and reads
// from its merged stdout/stderr.
func (r *Runtime) Attach(ctx context.Context, containerID string) (io.ReadWriteCloser, error) {
	// Use "docker start -ai" rather than separate start + attach so stdin is
	// connected from the moment the container process begins. This prevents
	// agents from seeing "the input device is not a TTY" on startup.
	cmd := exec.CommandContext(ctx, "docker", "start", "-ai", containerID)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}
	// Merge container stderr into the stdout pipe.
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("attaching to container: %w", err)
	}

	return &attachConn{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}

// ResizeTerminal resizes the pseudo-TTY of a running interactive container
// using docker exec stty.
func (r *Runtime) ResizeTerminal(ctx context.Context, containerID string, rows, cols int) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", containerID,
		"stty", "rows", strconv.Itoa(rows), "cols", strconv.Itoa(cols))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("resizing terminal: %s: %w", stderr.String(), err)
	}
	return nil
}

// attachConn implements io.ReadWriteCloser for a docker attach session,
// forwarding reads from the container's stdout and writes to its stdin.
type attachConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func (a *attachConn) Read(p []byte) (int, error) {
	return a.stdout.Read(p)
}

func (a *attachConn) Write(p []byte) (int, error) {
	return a.stdin.Write(p)
}

func (a *attachConn) Close() error {
	err := a.stdin.Close()
	a.stdout.Close()
	a.cmd.Wait()
	return err
}

// copyProjectToContainer creates a filtered tar archive of the project
// (honoring .clocheignore patterns) and pipes it into the container via
// "docker cp - container:/workspace/".
func copyProjectToContainer(ctx context.Context, projectDir, containerID string, patterns []ignorePattern) error {
	// If no ignore patterns, use the fast path (plain docker cp).
	if len(patterns) == 0 {
		cmd := exec.CommandContext(ctx, "docker", "cp", projectDir+"/.", containerID+":/workspace/")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("copying files to container: %s: %w", stderr.String(), err)
		}
		return nil
	}

	// Ensure /workspace/ exists in the container (docker cp - requires
	// the destination directory to exist already).
	initDir, err := os.MkdirTemp("", "cloche-init")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(initDir)
	exec.CommandContext(ctx, "docker", "cp", initDir+"/.", containerID+":/workspace/").Run()

	// Pipe a filtered tar into "docker cp -".
	cmd := exec.CommandContext(ctx, "docker", "cp", "-", containerID+":/workspace/")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	pipeR, pipeW := io.Pipe()
	cmd.Stdin = pipeR

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Run()
	}()

	// Write tar archive, skipping ignored files.
	tw := tar.NewWriter(pipeW)
	walkErr := filepath.Walk(projectDir, func(absPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(projectDir, absPath)
		if relPath == "." {
			return nil
		}
		// Normalize to forward slashes for matching.
		relPath = filepath.ToSlash(relPath)

		if isIgnored(patterns, relPath, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Read symlink target (if any) before creating the header.
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(absPath)
			if err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("creating tar header for %s: %w", relPath, err)
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(absPath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})

	// Close the tar writer and pipe even if walk failed, so docker cp
	// exits rather than hanging.
	tw.Close()
	pipeW.Close()

	cmdErr := <-errCh
	if walkErr != nil {
		return fmt.Errorf("building tar archive: %w", walkErr)
	}
	if cmdErr != nil {
		return fmt.Errorf("copying files to container: %s: %w", stderr.String(), cmdErr)
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