package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

func cmdExtract(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var id, atDir, branch string
	var noGit bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--at":
			if i+1 < len(args) {
				i++
				atDir = args[i]
			}
		case "--no-git":
			noGit = true
		case "--branch":
			if i+1 < len(args) {
				i++
				branch = args[i]
			}
		default:
			if id == "" && !strings.HasPrefix(args[i], "-") {
				id = args[i]
			}
		}
	}

	if id == "" {
		fmt.Fprintf(os.Stderr, "usage: cloche extract <id> [--at <dir>] [--no-git] [--branch <name>]\n")
		os.Exit(1)
	}

	// --no-git requires --at: enforce at the CLI before dialing the daemon.
	if noGit && atDir == "" {
		fmt.Fprintf(os.Stderr, "cloche extract: --no-git requires --at <dir>\n")
		os.Exit(1)
	}

	code := extractRun(ctx, client, id, atDir, branch, noGit, os.Stdout, os.Stderr)
	os.Exit(code)
}

// extractRun calls the ExtractRun RPC and prints the result.
// Returns 0 on success, 1 on error. Separated for testability.
func extractRun(ctx context.Context, client pb.ClocheServiceClient, id, atDir, branch string, noGit bool, stdout, stderr io.Writer) int {
	resp, err := client.ExtractRun(ctx, &pb.ExtractRunRequest{
		Id:     id,
		AtDir:  atDir,
		Branch: branch,
		NoGit:  noGit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cloche extract: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Extracted to: %s\n", resp.TargetDir)
	if resp.Branch != "" {
		fmt.Fprintf(stdout, "Branch: %s\n", resp.Branch)
	}
	return 0
}
