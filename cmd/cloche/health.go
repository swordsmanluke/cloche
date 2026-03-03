package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
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

func cmdHealth(args []string) {
	httpAddr := os.Getenv("CLOCHE_HTTP")
	if httpAddr == "" {
		fmt.Fprintf(os.Stderr, "error: CLOCHE_HTTP not set (e.g. export CLOCHE_HTTP=localhost:8080)\n")
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

func colorStatus(status string) string {
	if !isTTY() {
		return status
	}
	switch status {
	case "green":
		return "\033[32m" + status + "\033[0m"
	case "yellow":
		return "\033[33m" + status + "\033[0m"
	case "red":
		return "\033[31m" + status + "\033[0m"
	default:
		return status
	}
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
