package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/config"
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
	startTime := time.Now()
	log.Printf("runtime.Start: creating container (image=%s workflow=%s attempt=%s)", cfg.Image, cfg.WorkflowName, cfg.AttemptID)

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

	// Pass daemon gRPC address so in-container clo commands can reach the daemon.
	// The host is reachable at host.docker.internal (added via --add-host above).
	clocheAddr := os.Getenv("CLOCHE_ADDR")
	if clocheAddr == "" {
		clocheAddr = config.DefaultAddr()
	}
	// Convert localhost addresses to host.docker.internal for container access.
	containerAddr := clocheAddr
	containerAddr = strings.Replace(containerAddr, "127.0.0.1:", "host.docker.internal:", 1)
	containerAddr = strings.Replace(containerAddr, "0.0.0.0:", "host.docker.internal:", 1)
	args = append(args, "-e", "CLOCHE_ADDR="+containerAddr)

	// Claude auth files are copied (not mounted) after docker create so each
	// container gets its own copy — avoids concurrent write conflicts.

	// Bind-mount the host run directory (.cloche/runs/<run-id>) into the container
	// so that files written by host workflow steps (e.g. task_prompt.md written by
	// prepare-prompt.sh) are accessible to container steps via clo get task_prompt_path.
	// .cloche/runs/ is excluded from the project copy by .clocheignore, so without
	// this mount those files would be invisible inside the container.
	// ProjectDir is empty for resume containers (committed image has workspace state);
	// skip the mount in that case.
	if cfg.ProjectDir != "" && cfg.RunID != "" {
		hostRunDir := filepath.Join(cfg.ProjectDir, ".cloche", "runs", cfg.RunID)
		if err := os.MkdirAll(hostRunDir, 0755); err == nil {
			args = append(args, "-v", hostRunDir+":/workspace/.cloche/runs/"+cfg.RunID)
		}
	}

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
	log.Printf("runtime.Start: container created %s (%.1fs)", containerID, time.Since(startTime).Seconds())

	// 3. Copy project files into container, respecting .clocheignore
	if cfg.ProjectDir != "" {
		t := time.Now()
		log.Printf("runtime.Start: copying project files into %s", containerID)
		patterns, err := parseClocheignore(cfg.ProjectDir)
		if err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("parsing .clocheignore: %w", err)
		}

		if err := copyProjectToContainer(ctx, cfg.ProjectDir, containerID, patterns); err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", err
		}
		log.Printf("runtime.Start: project copy done for %s (%.1fs)", containerID, time.Since(t).Seconds())

		// Apply override files from .cloche/overrides/ on top of workspace
		overridesDir := filepath.Join(cfg.ProjectDir, ".cloche", "overrides")
		if _, err := os.Stat(overridesDir); err == nil {
			log.Printf("runtime.Start: applying overrides for %s", containerID)
			overrideCmd := exec.CommandContext(ctx, "docker", "cp", overridesDir+"/.", containerID+":/workspace/")
			var cpStderr bytes.Buffer
			overrideCmd.Stderr = &cpStderr
			if err := overrideCmd.Run(); err != nil {
				// Non-fatal: log but don't fail the run
				fmt.Fprintf(os.Stderr, "warning: copying overrides: %s\n", cpStderr.String())
			}
			log.Printf("runtime.Start: overrides done for %s", containerID)
		}
	}

	// 3b. Write prompt into container (.cloche/runs/ is excluded by .clocheignore,
	//      so prompt.txt must be injected separately).
	if cfg.Prompt != "" && cfg.TaskID != "" {
		log.Printf("runtime.Start: writing prompt for %s", containerID)
		promptDir := filepath.Join(os.TempDir(), "cloche-prompt-"+cfg.RunID)
		runsDir := filepath.Join(promptDir, ".cloche", "runs", cfg.TaskID)
		if err := os.MkdirAll(runsDir, 0755); err == nil {
			_ = os.WriteFile(filepath.Join(runsDir, "prompt.txt"), []byte(cfg.Prompt), 0644)
			cpCmd := exec.CommandContext(ctx, "docker", "cp", promptDir+"/.", containerID+":/workspace/")
			_ = cpCmd.Run()
			os.RemoveAll(promptDir)
		}
		log.Printf("runtime.Start: prompt done for %s", containerID)
	}

	// 4. Copy Claude auth files into container (each gets its own copy).
	// Only copy auth-relevant files — not the full ~/.claude directory
	// which contains large history, session, and debug data.
	log.Printf("runtime.Start: copying auth files for %s", containerID)
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
	log.Printf("runtime.Start: auth copy done for %s", containerID)

	// 5. Start the container (skip for interactive — Attach handles start).
	if !cfg.Interactive {
		log.Printf("runtime.Start: starting container %s", containerID)
		startCmd := exec.CommandContext(ctx, "docker", "start", containerID)
		var startStderr bytes.Buffer
		startCmd.Stderr = &startStderr
		if err := startCmd.Run(); err != nil {
			exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("starting container: %s: %w", startStderr.String(), err)
		}

		// Verify the container actually transitioned to running. docker start
		// can return success while the container remains in "created" state on
		// some hosts. Catching this here surfaces a concrete error instead of
		// letting SessionFor hang for the full step timeout.
		log.Printf("runtime.Start: verifying container %s reached running state", containerID)
		if err := r.waitForRunning(ctx, containerID); err != nil {
			exec.CommandContext(context.Background(), "docker", "rm", "-f", containerID).Run()
			return "", fmt.Errorf("container %s: %w", containerID, err)
		}
	}

	log.Printf("runtime.Start: container %s ready (total %.1fs)", containerID, time.Since(startTime).Seconds())
	return containerID, nil
}

// waitForRunning polls docker inspect until the container reports Running=true,
// returning an error if it does not transition within a short window. This
// catches the "container stuck in Created" failure mode where docker start
// returns success but the container never actually runs.
func (r *Runtime) waitForRunning(ctx context.Context, containerID string) error {
	const attempts = 5
	const interval = 300 * time.Millisecond

	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("waiting for running state: %w", ctx.Err())
			case <-time.After(interval):
			}
		}
		status, err := r.Inspect(ctx, containerID)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("waiting for running state: %w", ctx.Err())
			}
			continue // transient inspect error; retry
		}
		if status.Running {
			return nil
		}
	}

	if ctx.Err() != nil {
		return fmt.Errorf("waiting for running state: %w", ctx.Err())
	}

	// Fetch the actual status string for a meaningful error message.
	stateCmd := exec.CommandContext(context.Background(), "docker", "inspect",
		"--format", "{{.State.Status}}", containerID)
	stateOut, _ := stateCmd.Output()
	state := strings.TrimSpace(string(stateOut))
	if state == "" {
		state = "unknown"
	}
	return fmt.Errorf("stuck in %q state after start (not running)", state)
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

// CommitContainer creates a Docker image from the given container, preserving
// its filesystem state for cross-attempt resume. The image is tagged as
// "cloche-resume:<attemptID>-<containerID>" for traceability.
func (r *Runtime) CommitContainer(ctx context.Context, containerID, attemptID string) (string, error) {
	tag := "cloche-resume:" + attemptID + "-" + containerID
	cmd := exec.CommandContext(ctx, "docker", "commit", containerID, tag)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("committing container: %s: %w", stderr.String(), err)
	}
	return tag, nil
}

// RemoveImage removes a Docker image by tag.
func (r *Runtime) RemoveImage(ctx context.Context, imageTag string) error {
	cmd := exec.CommandContext(ctx, "docker", "rmi", imageTag)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("removing image %s: %s: %w", imageTag, stderr.String(), err)
	}
	return nil
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
	walkErr := writeTarFromProject(tw, projectDir, patterns)

	if walkErr != nil {
		// Tar build failed: do NOT write the end-of-archive marker. Closing
		// the pipe with an error signals docker cp that the stream is broken,
		// so it returns a non-zero exit code instead of treating the truncated
		// archive as successfully complete.
		pipeW.CloseWithError(walkErr)
	} else {
		tw.Close()
		pipeW.Close()
	}

	cmdErr := <-errCh
	if walkErr != nil {
		return fmt.Errorf("building tar archive: %w", walkErr)
	}
	if cmdErr != nil {
		return fmt.Errorf("copying files to container: %s: %w", stderr.String(), cmdErr)
	}
	// Surface any stderr output as an error even when docker cp exits 0.
	// docker cp can silently skip or truncate entries (tarslip protection,
	// ENOSPC mid-stream, Docker version differences) and still exit cleanly,
	// leaving the container workspace incomplete with no non-zero exit code.
	if stderrOut := strings.TrimSpace(stderr.String()); stderrOut != "" {
		return fmt.Errorf("copying files to container: %s", stderrOut)
	}
	return nil
}

// writeTarFromProject walks projectDir and writes matching files into tw,
// honoring patterns. Symlinks that point outside the project tree are
// dereferenced — their real content is inlined — so Docker's tarslip
// protection does not silently drop them and leave the container workspace
// incomplete.
func writeTarFromProject(tw *tar.Writer, projectDir string, patterns []ignorePattern) error {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("resolving project dir: %w", err)
	}

	return filepath.Walk(projectDir, func(absPath string, info os.FileInfo, err error) error {
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

		// Skip sockets, devices, and other special files that tar cannot represent.
		mode := info.Mode()
		if mode&(os.ModeSocket|os.ModeDevice|os.ModeNamedPipe|os.ModeCharDevice) != 0 {
			return nil
		}

		// Read symlink target (if any) before creating the header.
		var link string
		if mode&os.ModeSymlink != 0 {
			link, err = os.Readlink(absPath)
			if err != nil {
				return err
			}

			// Resolve the symlink to determine whether its target is inside
			// the project tree. External symlinks are dereferenced: Docker's
			// tarslip protection silently rejects tar entries whose link target
			// escapes the destination directory, causing the copy to truncate.
			target := link
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(absPath), target)
			}
			if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil && !isInsideDir(resolved, absProjectDir) {
				return addDereferencedEntry(tw, resolved, relPath)
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
}

// isInsideDir reports whether path is inside (or equal to) dir.
// Both should be absolute, clean paths.
func isInsideDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// addDereferencedEntry inlines the content of an external symlink target into
// the tar archive under relPath. For files the content is written as a regular
// file entry; for directories all contents are added recursively. This avoids
// creating symlink tar entries that Docker's tarslip protection would silently
// drop when the link target escapes /workspace/.
func addDereferencedEntry(tw *tar.Writer, resolvedTarget, relPath string) error {
	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return fmt.Errorf("external symlink %q: target inaccessible: %w", relPath, err)
	}

	if !info.IsDir() {
		hdr, herr := tar.FileInfoHeader(info, "")
		if herr != nil {
			return fmt.Errorf("creating tar header for %s: %w", relPath, herr)
		}
		hdr.Name = relPath
		if werr := tw.WriteHeader(hdr); werr != nil {
			return werr
		}
		if info.Mode().IsRegular() {
			f, ferr := os.Open(resolvedTarget)
			if ferr != nil {
				return ferr
			}
			defer f.Close()
			_, cerr := io.Copy(tw, f)
			return cerr
		}
		return nil
	}

	// Directory: walk and include all contents under relPath.
	return filepath.Walk(resolvedTarget, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		sub, _ := filepath.Rel(resolvedTarget, path)
		sub = filepath.ToSlash(sub)

		var entryPath string
		if sub == "." {
			entryPath = relPath
		} else {
			entryPath = relPath + "/" + sub
		}

		fMode := fi.Mode()
		if fMode&(os.ModeSocket|os.ModeDevice|os.ModeNamedPipe|os.ModeCharDevice) != 0 {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		var linkTarget string
		if fMode&os.ModeSymlink != 0 {
			var lerr error
			linkTarget, lerr = os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			// filepath.Walk does not follow symlinks, so nested symlinks that
			// escape resolvedTarget must be recursively dereferenced — emitting
			// them as plain symlink tar entries would re-trigger Docker's
			// tarslip silent-truncation for any target that escapes /workspace/.
			absTarget := linkTarget
			if !filepath.IsAbs(absTarget) {
				absTarget = filepath.Join(filepath.Dir(path), absTarget)
			}
			if innerResolved, innerErr := filepath.EvalSymlinks(absTarget); innerErr == nil && !isInsideDir(innerResolved, resolvedTarget) {
				return addDereferencedEntry(tw, innerResolved, entryPath)
			}
		}

		hdr, herr := tar.FileInfoHeader(fi, linkTarget)
		if herr != nil {
			return fmt.Errorf("creating tar header for %s: %w", entryPath, herr)
		}
		hdr.Name = entryPath

		if werr := tw.WriteHeader(hdr); werr != nil {
			return werr
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		f, ferr := os.Open(path)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		_, cerr := io.Copy(tw, f)
		return cerr
	})
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