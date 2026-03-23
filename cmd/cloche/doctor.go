package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// checkStatus represents the outcome of a single doctor check.
type checkStatus int

const (
	checkOK      checkStatus = iota
	checkWarning             // non-fatal
	checkFail                // fatal
)

// checkResult holds the outcome of a single doctor check.
type checkResult struct {
	label       string
	status      checkStatus
	detail      string // extra info shown in verbose mode or on failure
	remediation string // actionable guidance shown on failure
}

func (r checkResult) statusString() string {
	switch r.status {
	case checkOK:
		return "ok"
	case checkWarning:
		return "warning"
	default:
		return "FAIL"
	}
}

// doctorRunner holds state shared across all checks.
type doctorRunner struct {
	verbose    bool
	addr       string
	projectDir string
	timeout    time.Duration
}

func cmdDoctor(args []string) {
	var verbose bool
	var projectDir string
	timeout := 60 * time.Second

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--verbose", "-v":
			verbose = true
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--timeout":
			if i+1 < len(args) {
				i++
				if d, err := time.ParseDuration(args[i]); err == nil {
					timeout = d
				}
			}
		}
	}

	if projectDir == "" {
		projectDir, _ = os.Getwd()
	} else {
		if abs, err := filepath.Abs(projectDir); err == nil {
			projectDir = abs
		}
	}

	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}

	dr := &doctorRunner{
		verbose:    verbose,
		addr:       addr,
		projectDir: projectDir,
		timeout:    timeout,
	}

	results := []checkResult{
		dr.checkDocker(),
		dr.checkBaseImage(),
		dr.checkDaemon(),
		dr.checkAuth(),
	}

	// Project-level checks: only when inside a cloche project directory.
	clocheDir := filepath.Join(projectDir, ".cloche")
	if info, err := os.Stat(clocheDir); err == nil && info.IsDir() {
		results = append(results, dr.checkProjectConfig())
		results = append(results, dr.checkWorkflows())

		imageResult := dr.checkImageBuild()
		results = append(results, imageResult)

		// Only attempt roundtrip if the image build succeeded.
		if imageResult.status != checkFail {
			results = append(results, dr.checkAgentRoundtrip())
		}
	}

	dr.printResults(results)

	anyFail := false
	for _, r := range results {
		if r.status == checkFail {
			anyFail = true
			break
		}
	}
	if anyFail {
		os.Exit(1)
	}
}

// printResults prints a formatted table of check results.
func (dr *doctorRunner) printResults(results []checkResult) {
	const labelWidth = 40
	anyIssue := false

	for _, r := range results {
		label := r.label + "..."
		padding := labelWidth - len(label)
		if padding < 1 {
			padding = 1
		}
		statusStr := r.statusString()
		if r.status == checkOK && r.detail != "" && dr.verbose {
			statusStr = fmt.Sprintf("ok (%s)", r.detail)
		} else if r.status == checkOK && r.detail != "" {
			statusStr = fmt.Sprintf("ok (%s)", r.detail)
		}
		fmt.Printf("%s%s%s\n", label, strings.Repeat(" ", padding), statusStr)
		if r.status != checkOK {
			anyIssue = true
			if r.remediation != "" {
				for _, line := range strings.Split(r.remediation, "\n") {
					fmt.Printf("  %s\n", line)
				}
			}
		} else if dr.verbose && r.detail != "" {
			// already included in statusStr above
		}
	}

	fmt.Println()
	if !anyIssue {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed. See above for remediation steps.")
	}
}

// checkDocker verifies that the Docker daemon is reachable by running `docker info`.
func (dr *doctorRunner) checkDocker() checkResult {
	label := "Checking Docker"
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		var remediation string
		if runtime.GOOS == "darwin" {
			remediation = "Is Docker Desktop running? Start it from the Applications folder or menubar."
		} else {
			remediation = "Is the docker service started? Try: sudo systemctl start docker"
		}
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      err.Error(),
			remediation: remediation,
		}
	}
	return checkResult{label: label, status: checkOK}
}

// checkBaseImage verifies that cloche-base:latest (or cloche-agent:latest) exists locally.
func (dr *doctorRunner) checkBaseImage() checkResult {
	label := "Checking base image"

	// Try cloche-base:latest first, then cloche-agent:latest for older setups.
	images := []string{"cloche-base:latest", "cloche-agent:latest"}
	for _, image := range images {
		cmd := exec.Command("docker", "image", "inspect", image)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err == nil {
			return checkResult{
				label:  label,
				status: checkOK,
				detail: image,
			}
		}
	}

	return checkResult{
		label:  label,
		status: checkFail,
		detail: "cloche-base:latest not found",
		remediation: "Build the base image with: make docker-base\n" +
			"Or pull it from your registry if distributed separately.",
	}
}

// checkDaemon verifies the daemon is reachable by calling GetVersion over gRPC.
func (dr *doctorRunner) checkDaemon() checkResult {
	label := fmt.Sprintf("Checking daemon (%s)", dr.addr)

	conn, err := grpc.NewClient(dr.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return checkResult{
			label:  label,
			status: checkFail,
			detail: err.Error(),
			remediation: "Start the daemon with: cloched\n" +
				"Or try: cloche shutdown --restart",
		}
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.GetVersion(ctx, &pb.GetVersionRequest{})
	if err != nil {
		return checkResult{
			label:  label,
			status: checkFail,
			detail: err.Error(),
			remediation: "The daemon is not responding. Try:\n" +
				"  cloched            # start the daemon\n" +
				"  cloche shutdown --restart\n" +
				"Check whether another process holds port " + dr.addr,
		}
	}

	return checkResult{
		label:  label,
		status: checkOK,
		detail: "v" + resp.Version,
	}
}

// checkAuth verifies that agent authentication credentials are present.
// This is a soft check (warning, not fatal) since auth mechanisms vary by agent.
func (dr *doctorRunner) checkAuth() checkResult {
	label := "Checking agent auth"

	// Check ANTHROPIC_API_KEY env var first.
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return checkResult{
			label:  label,
			status: checkOK,
			detail: "ANTHROPIC_API_KEY set",
		}
	}

	// Check for Claude Code session data in ~/.claude/
	home, err := os.UserHomeDir()
	if err == nil {
		claudeDir := filepath.Join(home, ".claude")
		if info, err := os.Stat(claudeDir); err == nil && info.IsDir() {
			// Check for any session-related files.
			entries, _ := os.ReadDir(claudeDir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") || e.Name() == "credentials" || e.Name() == "session" {
					return checkResult{
						label:  label,
						status: checkOK,
						detail: "~/.claude/ session data found",
					}
				}
			}
			// Directory exists but no session files.
			if len(entries) > 0 {
				return checkResult{
					label:  label,
					status: checkOK,
					detail: "~/.claude/ directory found",
				}
			}
		}
	}

	return checkResult{
		label:  label,
		status: checkWarning,
		detail: "no credentials found",
		remediation: "Set ANTHROPIC_API_KEY for API key auth, or run 'claude' to authenticate interactively.\n" +
			"If using a different agent, ensure its credentials are configured.",
	}
}

// checkProjectConfig loads .cloche/config.toml and checks for common issues.
func (dr *doctorRunner) checkProjectConfig() checkResult {
	label := "Checking project config"

	cfg, err := config.Load(dr.projectDir)
	if err != nil {
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      err.Error(),
			remediation: "Fix the parse error in .cloche/config.toml",
		}
	}

	var warnings []string

	if !cfg.Active {
		warnings = append(warnings, "active = false (project is inactive; set active = true to enable)")
	}

	if cfg.Daemon.Image == "" {
		warnings = append(warnings, "no image configured in [daemon] image (will use default cloche-agent:latest)")
	} else if strings.ContainsAny(cfg.Daemon.Image, " \t\n") {
		warnings = append(warnings, "image name contains whitespace: "+cfg.Daemon.Image)
	}

	// Scan for TODO(cloche-init) markers in .cloche/ files.
	clocheDir := filepath.Join(dr.projectDir, ".cloche")
	todoCount := scanForTODOMarkers(clocheDir)
	if todoCount > 0 {
		warnings = append(warnings, fmt.Sprintf("%d file(s) still contain TODO(cloche-init) placeholders — run 'cloche init' to fill them in", todoCount))
	}

	if len(warnings) > 0 {
		return checkResult{
			label:       label,
			status:      checkWarning,
			detail:      strings.Join(warnings, "; "),
			remediation: strings.Join(warnings, "\n"),
		}
	}

	return checkResult{label: label, status: checkOK}
}

// scanForTODOMarkers counts files under clocheDir containing TODO(cloche-init) markers.
func scanForTODOMarkers(clocheDir string) int {
	patterns := []string{
		filepath.Join(clocheDir, "*.cloche"),
		filepath.Join(clocheDir, "Dockerfile"),
		filepath.Join(clocheDir, "prompts", "*.md"),
	}
	count := 0
	for _, pattern := range patterns {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err == nil && strings.Contains(string(data), "TODO(cloche-init)") {
				count++
			}
		}
	}
	return count
}

// checkWorkflows validates all .cloche/*.cloche workflow files by reusing
// the same logic as `cloche validate`.
func (dr *doctorRunner) checkWorkflows() checkResult {
	label := "Checking workflow syntax"

	errs := validateProject(dr.projectDir, "")
	if len(errs) > 0 {
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      fmt.Sprintf("%d error(s)", len(errs)),
			remediation: strings.Join(errs, "\n"),
		}
	}

	return checkResult{label: label, status: checkOK}
}

// checkImageBuild ensures the project Docker image is built and up-to-date.
func (dr *doctorRunner) checkImageBuild() checkResult {
	label := "Checking project image build"

	cfg, err := config.Load(dr.projectDir)
	if err != nil {
		return checkResult{
			label:  label,
			status: checkFail,
			detail: "cannot load config: " + err.Error(),
		}
	}

	image := cfg.Daemon.Image
	if image == "" {
		image = strings.ToLower(filepath.Base(dr.projectDir)) + "-cloche-agent:latest"
	}

	dockerfilePath := filepath.Join(dr.projectDir, ".cloche", "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      ".cloche/Dockerfile not found",
			remediation: "Create a Dockerfile at .cloche/Dockerfile, or run 'cloche init' to scaffold one.",
		}
	}

	rt, err := docker.NewRuntime()
	if err != nil {
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      err.Error(),
			remediation: "Docker is required to build the project image.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := rt.EnsureImage(ctx, dr.projectDir, image); err != nil {
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      err.Error(),
			remediation: "Fix the Docker build error shown above and retry:\n  cloche doctor --project " + dr.projectDir,
		}
	}

	return checkResult{
		label:  label,
		status: checkOK,
		detail: image,
	}
}

// testWorkflowContent is the minimal workflow used for the agent roundtrip check.
const testWorkflowContent = `workflow "doctor-test" {
  step test {
    run     = "echo ok > /tmp/doctor-test"
    results = [success, fail]
  }

  test:success -> done
  test:fail    -> abort
}
`

// checkAgentRoundtrip starts a container from the project image, runs a minimal
// agent workflow, and verifies it completes within the configured timeout.
func (dr *doctorRunner) checkAgentRoundtrip() checkResult {
	label := "Checking agent roundtrip"

	cfg, err := config.Load(dr.projectDir)
	if err != nil {
		return checkResult{
			label:  label,
			status: checkFail,
			detail: "cannot load config: " + err.Error(),
		}
	}

	image := cfg.Daemon.Image
	if image == "" {
		image = strings.ToLower(filepath.Base(dr.projectDir)) + "-cloche-agent:latest"
	}

	// Create a temporary workspace with a minimal test workflow.
	tmpDir, err := os.MkdirTemp("", "cloche-doctor")
	if err != nil {
		return checkResult{label: label, status: checkFail, detail: err.Error()}
	}
	defer os.RemoveAll(tmpDir)

	clocheSubDir := filepath.Join(tmpDir, ".cloche")
	if err := os.MkdirAll(clocheSubDir, 0755); err != nil {
		return checkResult{label: label, status: checkFail, detail: err.Error()}
	}

	if err := os.WriteFile(filepath.Join(clocheSubDir, "doctor-test.cloche"), []byte(testWorkflowContent), 0644); err != nil {
		return checkResult{label: label, status: checkFail, detail: err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), dr.timeout)
	defer cancel()

	start := time.Now()

	// Run a short-lived container with the test workflow.
	// No CLOCHE_ADDR is set so the agent skips daemon KV operations.
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", tmpDir+":/workspace",
		"-w", "/workspace",
		image,
		"cloche-agent", ".cloche/doctor-test.cloche",
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	runErr := cmd.Run()
	elapsed := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return checkResult{
			label:  label,
			status: checkFail,
			detail: fmt.Sprintf("timed out after %v", dr.timeout),
			remediation: "The agent container did not complete within " + dr.timeout.String() + ".\n" +
				"Check whether the agent binary starts correctly:\n" +
				"  docker run --rm " + image + " cloche-agent --version",
		}
	}

	if runErr != nil {
		logs := strings.TrimSpace(out.String())
		if len(logs) > 800 {
			logs = logs[:800] + "\n..."
		}
		return checkResult{
			label:       label,
			status:      checkFail,
			detail:      runErr.Error(),
			remediation: "Container output:\n" + logs,
		}
	}

	return checkResult{
		label:  label,
		status: checkOK,
		detail: fmt.Sprintf("agent responded in %v", elapsed.Round(time.Second)),
	}
}
