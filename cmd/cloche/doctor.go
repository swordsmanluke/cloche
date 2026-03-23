package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
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
	verbose bool
	addr    string
}

func cmdDoctor(args []string) {
	var verbose bool
	for _, arg := range args {
		switch arg {
		case "--verbose", "-v":
			verbose = true
		}
	}

	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}

	dr := &doctorRunner{verbose: verbose, addr: addr}

	results := []checkResult{
		dr.checkDocker(),
		dr.checkBaseImage(),
		dr.checkDaemon(),
		dr.checkAuth(),
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
