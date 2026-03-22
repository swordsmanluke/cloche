package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// completionSubcommands is the canonical list of all cloche subcommands.
var completionSubcommands = []string{
	"complete", "delete", "get", "health", "help", "init", "list", "logs",
	"loop", "poll", "project", "resume", "run", "set", "shutdown", "status",
	"stop", "tasks", "validate", "workflow",
}

// cmdComplete handles `cloche complete --index <n> -- <word0> <word1> ...`.
// It prints one completion candidate per line.
// If the daemon is available, dynamic completions (task IDs, workflow names,
// etc.) are returned via the Complete gRPC RPC. Otherwise it falls back to
// static completions.
func cmdComplete(args []string) {
	var index int
	var words []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--index", "-i":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &index)
			}
		case "--":
			words = args[i+1:]
			i = len(args) // stop flag parsing
		default:
			words = append(words, args[i])
		}
		i++
	}

	completions := resolveCompletions(index, words)
	for _, c := range completions {
		fmt.Println(c)
	}
}

// resolveCompletions returns completion candidates for the given cursor position
// and command-line words. It first attempts to query the daemon; on failure it
// falls back to local static completions.
func resolveCompletions(index int, words []string) []string {
	// words[0] is "cloche" (or sometimes absent in test contexts).
	// index=0 → completing "cloche" itself (unused in practice).
	// index=1 → completing the subcommand.
	// index>=2 → completing an argument of words[1].

	cur := ""
	if index > 0 && index < len(words) {
		cur = words[index]
	}

	if index <= 1 {
		return completionFilterPrefix(completionSubcommands, cur)
	}

	if len(words) < 2 {
		return nil
	}

	// Try daemon first.
	cwd, _ := os.Getwd()
	if dynamic := queryDaemonCompletions(index, words, cwd); dynamic != nil {
		return dynamic
	}

	// Static fallback.
	return staticCompletions(words[1], index, words, cur)
}

// queryDaemonCompletions contacts the daemon and calls the Complete RPC.
// Returns nil if the daemon is unavailable or returns an error.
func queryDaemonCompletions(index int, words []string, projectDir string) []string {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &pb.CompleteRequest{
		Words:      words,
		CurIdx:     int32(index),
		ProjectDir: projectDir,
	})
	if err != nil {
		return nil
	}
	return resp.Completions
}

// staticCompletions returns flag/value completions based purely on the
// subcommand and surrounding tokens, without daemon access.
func staticCompletions(subcommand string, index int, words []string, cur string) []string {
	prev := ""
	if index > 1 && index-1 < len(words) {
		prev = words[index-1]
	}

	var candidates []string

	switch subcommand {
	case "run":
		switch prev {
		case "--workflow":
			// Workflow names: try reading from local .cloche/
			candidates = localWorkflowNames()
		default:
			candidates = []string{"--workflow", "--prompt", "-p", "--title", "--issue", "-i", "--keep-container"}
		}

	case "status":
		candidates = []string{"--all"}

	case "logs":
		switch prev {
		case "--type":
			candidates = []string{"full", "script", "llm"}
		case "--step", "-s":
			// no static step names available
		default:
			candidates = []string{"--step", "-s", "--type", "--follow", "-f", "--limit", "-l"}
		}

	case "list":
		switch prev {
		case "--state", "-s":
			candidates = []string{"running", "pending", "succeeded", "failed", "cancelled"}
		default:
			candidates = []string{"--all", "--runs", "--state", "-s", "--project", "-p", "--limit", "-n"}
		}

	case "loop":
		candidates = []string{"stop", "resume", "--max"}

	case "workflow":
		// Try reading from local .cloche/
		candidates = localWorkflowNames()

	case "shutdown":
		candidates = []string{"--force", "-f"}

	case "init":
		switch prev {
		case "--base-image":
			// no static candidates
		case "--workflow":
			// no static candidates
		default:
			candidates = []string{"--workflow", "--base-image"}
		}

	case "validate":
		// no flags yet

	case "health", "project", "tasks":
		// no flags
	}

	return completionFilterPrefix(candidates, cur)
}

// localWorkflowNames reads workflow names from .cloche/*.cloche in the cwd.
func localWorkflowNames() []string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	infos, err := discoverWorkflows(cwd)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.name)
	}
	return names
}

// completionFilterPrefix returns items from list that start with prefix.
// If prefix is empty, all items are returned.
func completionFilterPrefix(list []string, prefix string) []string {
	if prefix == "" {
		return list
	}
	var out []string
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}
