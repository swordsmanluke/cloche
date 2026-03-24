package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

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

	url := "http://" + httpAddr + "/api/projects"
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

