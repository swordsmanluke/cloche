package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
)

type healthData struct {
	Status string `json:"status"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	Total  int    `json:"total"`
}

type projectHealth struct {
	Dir    string     `json:"dir"`
	Label  string     `json:"label"`
	Health healthData `json:"health"`
}

// resolveHTTPAddr returns the daemon's HTTP address by checking, in order:
// 1. CLOCHE_HTTP environment variable
// 2. [daemon] http in ~/.config/cloche/config
// Returns empty string if neither is set.
func resolveHTTPAddr() string {
	if v := os.Getenv("CLOCHE_HTTP"); v != "" {
		return strings.TrimPrefix(v, "http://")
	}
	if cfg, err := config.LoadGlobal(); err == nil && cfg.Daemon.HTTP != "" {
		return strings.TrimPrefix(cfg.Daemon.HTTP, "http://")
	}
	return ""
}

func cmdHealth(args []string) {
	httpAddr := resolveHTTPAddr()
	if httpAddr == "" {
		fmt.Fprintf(os.Stderr, "error: cannot determine daemon HTTP address\n")
		fmt.Fprintf(os.Stderr, "hint: set CLOCHE_HTTP or configure [daemon] http in ~/.config/cloche/config\n")
		os.Exit(1)
	}

	// Optional --project <dir> scopes the summary to one project; the daemon
	// maps the directory to its registered project, so this works the same
	// way regardless of what slug/label it was assigned.
	projectDir := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--project" && i+1 < len(args) {
			i++
			projectDir = args[i]
		}
	}

	reqURL := "http://" + httpAddr + "/api/projects"
	if projectDir != "" {
		reqURL += "?project=" + url.QueryEscape(projectDir)
	}
	resp, err := http.Get(reqURL)
	if err != nil {
		// The dial failure looks identical whether the whole daemon is down
		// or just its web dashboard (e.g. stuck retrying a bind failure).
		// Check gRPC, which is a separate listener, to tell them apart.
		reportWebUnreachable(err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "error: server returned %s\n", resp.Status)
		os.Exit(1)
	}

	var projects []projectHealth
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tSTATUS\tPASSED\tFAILED\tTOTAL")
	for _, p := range projects {
		name := p.Label
		if name == "" {
			name = p.Dir
		}
		status := colorStatus(p.Health.Status)
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n",
			name, status, p.Health.Passed, p.Health.Failed, p.Health.Total)
	}
	w.Flush()
}

// reportWebUnreachable prints an error for a failed web dashboard request,
// enriched with the daemon's gRPC-reported web status when reachable. This
// distinguishes "daemon is fully down" from "daemon is up but its web
// dashboard bind failed" — the latter looks identical over plain HTTP.
func reportWebUnreachable(httpErr error) {
	conn, err := dialDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", httpErr)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	verResp, err := pb.NewClocheServiceClient(conn).GetVersion(ctx, &pb.GetVersionRequest{})
	if err != nil {
		// gRPC unreachable too: the daemon itself is down.
		fmt.Fprintf(os.Stderr, "error: %v\n", httpErr)
		return
	}

	if verResp.WebAddr != "" && !verResp.WebUp {
		detail := verResp.WebError
		if detail == "" {
			detail = "bind failed"
		}
		fmt.Fprintf(os.Stderr, "error: web dashboard is down (%s); daemon is otherwise healthy (version %s)\n", detail, verResp.Version)
		fmt.Fprintf(os.Stderr, "hint: it retries automatically; run 'cloche status' for details\n")
		return
	}

	fmt.Fprintf(os.Stderr, "error: %v\n", httpErr)
}

