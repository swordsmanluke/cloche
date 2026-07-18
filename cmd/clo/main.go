// Command clo is a lightweight in-container CLI for the cloche KV store.
// It reads CLOCHE_ADDR, CLOCHE_TASK_ID, and CLOCHE_ATTEMPT_ID from the
// environment, dials the daemon's gRPC endpoint, and calls the context KV RPCs.
//
// Usage:
//
//	clo get <key>              Print value to stdout; exit 1 if not found
//	clo set <key> <value>      Set a key
//	clo set <key> -            Read value from stdin
//	clo set <key> -f <file>    Set a key from file contents
//	clo keys                   List all keys in the current attempt namespace
//	clo ask [--thread <id>] [--option A --option B ...] [--key <ask-key>] "question"
//	                           Ask the user a question and block for the reply
//	clo -v / clo --version     Print version
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if os.Args[1] == "-v" || os.Args[1] == "--version" || os.Args[1] == "version" {
		fmt.Printf("clo %s\n", version.Version())
		return
	}

	switch os.Args[1] {
	case "get":
		cmdGet(os.Args[2:])
	case "set":
		cmdSet(os.Args[2:])
	case "keys":
		cmdKeys()
	case "ask":
		cmdAsk(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "usage: clo <command> [args]\n")
	fmt.Fprintf(os.Stderr, "  get <key>              Print value; exit 1 if not found\n")
	fmt.Fprintf(os.Stderr, "  set <key> <value>      Set a key\n")
	fmt.Fprintf(os.Stderr, "  set <key> -            Read value from stdin\n")
	fmt.Fprintf(os.Stderr, "  set <key> -f <file>    Set a key from file contents\n")
	fmt.Fprintf(os.Stderr, "  keys                   List all keys\n")
	fmt.Fprintf(os.Stderr, "  ask [--thread <id>] [--option A --option B ...] [--key <ask-key>] \"question\"\n")
	fmt.Fprintf(os.Stderr, "                         Ask the user a question and block for the reply\n")
	fmt.Fprintf(os.Stderr, "  -v / --version         Print version\n")
}

// askParkedExitCode is returned when the run was parked mid-ask (the step is
// being torn down; on replay the same call returns the answer instantly).
const askParkedExitCode = 3

func cmdAsk(args []string) {
	var threadID, askKey string
	var options []string
	var questionParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--thread":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "--thread requires a value\n")
				os.Exit(1)
			}
			threadID = args[i]
		case "--option":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "--option requires a value\n")
				os.Exit(1)
			}
			options = append(options, args[i])
		case "--key":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "--key requires a value\n")
				os.Exit(1)
			}
			askKey = args[i]
		default:
			questionParts = append(questionParts, args[i])
		}
	}
	question := strings.TrimSpace(strings.Join(questionParts, " "))
	if question == "" {
		fmt.Fprintf(os.Stderr, "usage: clo ask [--thread <id>] [--option A --option B ...] [--key <ask-key>] \"question\"\n")
		os.Exit(1)
	}

	conn, taskID, attemptID, runID := dial()
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	// AskHelp blocks until a reply arrives (up to the daemon's park_after grace
	// period), so this needs a much longer timeout than the other clo commands.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	resp, err := client.AskHelp(ctx, &pb.AskHelpRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
		Question:  question,
		ThreadId:  threadID,
		Options:   options,
		AskKey:    askKey,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if resp.Parked {
		fmt.Fprintf(os.Stderr, "run parked awaiting reply on thread %s; the answer will be delivered on resume\n", resp.ThreadId)
		os.Exit(askParkedExitCode)
	}
	fmt.Println(resp.Answer)
}

func dial() (*grpc.ClientConn, string, string, string) {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		fmt.Fprintf(os.Stderr, "CLOCHE_ADDR is not set\n")
		os.Exit(1)
	}
	taskID := os.Getenv("CLOCHE_TASK_ID")
	if taskID == "" {
		fmt.Fprintf(os.Stderr, "CLOCHE_TASK_ID is not set\n")
		os.Exit(1)
	}
	attemptID := os.Getenv("CLOCHE_ATTEMPT_ID")
	runID := os.Getenv("CLOCHE_RUN_ID")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to daemon at %s: %v\n", addr, err)
		os.Exit(1)
	}
	return conn, taskID, attemptID, runID
}

func cmdGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: clo get <key>\n")
		os.Exit(1)
	}

	conn, taskID, attemptID, runID := dial()
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetContextKey(ctx, &pb.GetContextKeyRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
		Key:       args[0],
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Found {
		fmt.Fprintf(os.Stderr, "key not found: %s\n", args[0])
		os.Exit(1)
	}
	fmt.Println(resp.Value)
}

func cmdSet(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: clo set <key> <value>\n")
		fmt.Fprintf(os.Stderr, "       clo set <key> -            (read from stdin)\n")
		fmt.Fprintf(os.Stderr, "       clo set <key> -f <file>\n")
		os.Exit(1)
	}

	key := args[0]
	var value string
	switch {
	case args[1] == "-f" && len(args) >= 3:
		data, err := os.ReadFile(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			os.Exit(1)
		}
		value = string(data)
	case args[1] == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		// Trim trailing newline (consistent with cloche set behaviour)
		for len(data) > 0 && data[len(data)-1] == '\n' {
			data = data[:len(data)-1]
		}
		value = string(data)
	default:
		value = args[1]
	}

	conn, taskID, attemptID, runID := dial()
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.SetContextKey(ctx, &pb.SetContextKeyRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
		Key:       key,
		Value:     value,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdKeys() {
	conn, taskID, attemptID, runID := dial()
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListContextKeys(ctx, &pb.ListContextKeysRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, k := range resp.Keys {
		fmt.Println(k)
	}
}
