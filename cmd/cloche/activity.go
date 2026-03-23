package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/config"
)

// cmdActivity reads the project's activity log from the daemon's SQLite
// database and displays it.
// Usage:
//
//	cloche activity [--project <dir>] [--since <duration>] [--until <time>] [--json]
func cmdActivity(args []string) {
	var projectDir string
	var sinceStr string
	var untilStr string
	var asJSON bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--since":
			if i+1 < len(args) {
				i++
				sinceStr = args[i]
			}
		case "--until":
			if i+1 < len(args) {
				i++
				untilStr = args[i]
			}
		case "--json":
			asJSON = true
		}
	}

	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		projectDir = cwd
	}

	opts := activitylog.ReadOptions{}

	if sinceStr != "" {
		t, err := parseSinceArg(sinceStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --since value %q: %v\n", sinceStr, err)
			os.Exit(1)
		}
		opts.Since = t
	}
	if untilStr != "" {
		t, err := time.Parse(time.RFC3339, untilStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --until value %q (expected RFC3339, e.g. 2026-03-22T14:00:00Z): %v\n", untilStr, err)
			os.Exit(1)
		}
		opts.Until = t
	}

	dbPath := envOrDefault("CLOCHE_DB", config.DefaultDBPath())
	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	entries, err := store.ReadActivityEntries(context.Background(), projectDir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading activity log: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no activity log entries found")
		return
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		for _, e := range entries {
			_ = enc.Encode(e)
		}
		return
	}

	printActivityEntries(entries)
}

// envOrDefault returns the value of env var key, or fallback if unset.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseSinceArg parses a --since value as either a Go duration (e.g. "24h", "7d")
// or an RFC3339 timestamp.
func parseSinceArg(s string) (time.Time, error) {
	// Try duration first (e.g. "24h", "7d", "30m").
	// Go's time.ParseDuration doesn't support "d", so handle it manually.
	if len(s) > 1 && s[len(s)-1] == 'd' {
		dayStr := s[:len(s)-1]
		var days int
		if _, err := fmt.Sscanf(dayStr, "%d", &days); err == nil {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	// Fall back to RFC3339.
	return time.Parse(time.RFC3339, s)
}

func printActivityEntries(entries []activitylog.Entry) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tKIND\tTASK\tATTEMPT\tWORKFLOW\tSTEP\tOUTCOME")

	for _, e := range entries {
		ts := e.Timestamp.Local().Format("2006-01-02 15:04:05")
		outcome := e.Result
		if outcome == "" {
			outcome = e.State
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ts,
			string(e.Kind),
			e.TaskID,
			e.AttemptID,
			e.WorkflowName,
			e.StepName,
			outcome,
		)
	}
	w.Flush()
}
