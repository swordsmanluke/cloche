package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cloche-dev/cloche/internal/config"
)

// resolveDebugAddr returns the daemon's debug HTTP address from, in order:
// a --debug-addr flag in args, the CLOCHE_DEBUG env var, or [daemon] debug
// in the global config. Returns empty string if none is configured.
func resolveDebugAddr(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--debug-addr" {
			return args[i+1]
		}
	}
	if v := os.Getenv("CLOCHE_DEBUG"); v != "" {
		return strings.TrimPrefix(v, "http://")
	}
	if cfg, err := config.LoadGlobal(); err == nil && cfg.Daemon.Debug != "" {
		return strings.TrimPrefix(cfg.Daemon.Debug, "http://")
	}
	return ""
}

func cmdDebug(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`usage: cloche debug <subcommand> [--debug-addr <addr>]

Subcommands:
  goroutines   Print a full goroutine stack dump from the running daemon
  state        Print a JSON summary of active runs, loops, and container sessions

The debug server must be enabled on cloched:
  cloched --debug-addr localhost:7778
  CLOCHE_DEBUG=localhost:7778 cloched
  # or in ~/.config/cloche/config: [daemon] debug = "localhost:7778"
`)
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}

	switch args[0] {
	case "goroutines", "dump":
		cmdDebugGoroutines(args[1:])
	case "state":
		cmdDebugState(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown debug subcommand: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "run 'cloche debug --help' for usage\n")
		os.Exit(1)
	}
}

func requireDebugAddr(args []string) string {
	addr := resolveDebugAddr(args)
	if addr == "" {
		fmt.Fprintf(os.Stderr, "error: debug server address not configured\n")
		fmt.Fprintf(os.Stderr, "start cloched with:  --debug-addr localhost:7778\n")
		fmt.Fprintf(os.Stderr, "or set:              CLOCHE_DEBUG=localhost:7778\n")
		fmt.Fprintf(os.Stderr, "or add to config:    [daemon] debug = \"localhost:7778\"\n")
		os.Exit(1)
	}
	return addr
}

func cmdDebugGoroutines(args []string) {
	addr := requireDebugAddr(args)
	url := "http://" + addr + "/debug/pprof/goroutine?debug=2"
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "error reading response: %v\n", err)
		os.Exit(1)
	}
}

// daemonStateSnapshot mirrors adaptgrpc.DaemonStateSnapshot for JSON decoding.
type daemonStateSnapshot struct {
	GoroutineCount    int               `json:"goroutine_count"`
	ActiveRunIDs      []string          `json:"active_run_ids"`
	ActiveLoops       []string          `json:"active_loops"`
	ContainerSessions []sessionSnapshot `json:"container_sessions"`
}

type sessionSnapshot struct {
	AttemptID    string `json:"attempt_id"`
	ContainerID  string `json:"container_id"`
	PendingSteps int    `json:"pending_steps"`
}

func cmdDebugState(args []string) {
	addr := requireDebugAddr(args)
	url := "http://" + addr + "/debug/state"
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var snap daemonStateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		fmt.Fprintf(os.Stderr, "error decoding response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Goroutines: %d\n", snap.GoroutineCount)

	if len(snap.ActiveLoops) > 0 {
		fmt.Printf("\nActive loops (%d):\n", len(snap.ActiveLoops))
		for _, dir := range snap.ActiveLoops {
			fmt.Printf("  %s\n", dir)
		}
	}

	if len(snap.ActiveRunIDs) > 0 {
		fmt.Printf("\nActive runs (%d):\n", len(snap.ActiveRunIDs))
		for _, id := range snap.ActiveRunIDs {
			fmt.Printf("  %s\n", id)
		}
	}

	if len(snap.ContainerSessions) > 0 {
		fmt.Printf("\nContainer sessions (%d):\n", len(snap.ContainerSessions))
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  ATTEMPT\tCONTAINER\tPENDING")
		for _, s := range snap.ContainerSessions {
			cid := s.ContainerID
			if len(cid) > 12 {
				cid = cid[:12]
			}
			fmt.Fprintf(w, "  %s\t%s\t%d\n", s.AttemptID, cid, s.PendingSteps)
		}
		w.Flush()
	}

	if len(snap.ActiveLoops) == 0 && len(snap.ActiveRunIDs) == 0 && len(snap.ContainerSessions) == 0 {
		fmt.Println("\nNo active loops, runs, or container sessions.")
	}
}
