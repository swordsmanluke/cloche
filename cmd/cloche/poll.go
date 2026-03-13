package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

func cmdPoll(client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche poll <run-id> [run-id...]\n")
		os.Exit(1)
	}

	if len(args) == 1 {
		cmdPollSingle(client, args[0])
	} else {
		exitCode := cmdPollMulti(client, args, os.Stdout, os.Stderr)
		os.Exit(exitCode)
	}
}

func cmdPollSingle(client pb.ClocheServiceClient, runID string) {
	pollInterval := 2 * time.Second
	containerDeadThreshold := 1 * time.Minute

	var lastStepCount int
	var lastState string

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{RunId: runID})
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Print new step events
		for i := lastStepCount; i < len(resp.StepExecutions); i++ {
			exec := resp.StepExecutions[i]
			ts := time.Now().Format("15:04:05")
			if exec.Result == "" {
				fmt.Printf("[%s] Step %q started\n", ts, exec.StepName)
			} else {
				fmt.Printf("[%s] Step %q completed: %s\n", ts, exec.StepName, exec.Result)
			}
		}
		lastStepCount = len(resp.StepExecutions)

		// Print state changes
		if resp.State != lastState {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("[%s] Run %s is %s\n", ts, runID, resp.State)
			lastState = resp.State
		}

		// Check terminal states
		switch resp.State {
		case "succeeded":
			os.Exit(0)
		case "failed", "cancelled":
			if resp.ErrorMessage != "" {
				fmt.Fprintf(os.Stderr, "Error: %s\n", resp.ErrorMessage)
			}
			os.Exit(1)
		}

		// Check container death
		if !resp.ContainerAlive && resp.ContainerDeadSince != "" {
			deadSince, err := time.Parse(time.RFC3339Nano, resp.ContainerDeadSince)
			if err == nil && time.Since(deadSince) > containerDeadThreshold {
				fmt.Fprintf(os.Stderr, "[%s] Container has been dead for >1 minute (since %s)\n",
					time.Now().Format("15:04:05"), deadSince.Format("15:04:05"))
				os.Exit(1)
			}
		}

		time.Sleep(pollInterval)
	}
}

// isTerminalState returns true if the run state is a terminal state.
func isTerminalState(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}

// cmdPollMulti polls multiple runs and displays a compact status summary.
// Returns 0 if all runs succeeded, 1 if any failed or were cancelled.
func cmdPollMulti(client pb.ClocheServiceClient, runIDs []string, stdout, stderr io.Writer) int {
	pollInterval := 2 * time.Second
	states := make(map[string]string, len(runIDs))

	for {
		changed := false
		for _, id := range runIDs {
			if isTerminalState(states[id]) {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{RunId: id})
			cancel()

			if err != nil {
				fmt.Fprintf(stderr, "error polling %s: %v\n", id, err)
				states[id] = "error"
				changed = true
				continue
			}

			if resp.State != states[id] {
				states[id] = resp.State
				changed = true
			}
		}

		if changed {
			for _, id := range runIDs {
				fmt.Fprintf(stdout, "%s: %s\n", id, states[id])
			}
			fmt.Fprintln(stdout)
		}

		// Check if all runs are in terminal states
		allDone := true
		for _, id := range runIDs {
			if !isTerminalState(states[id]) && states[id] != "error" {
				allDone = false
				break
			}
		}

		if allDone {
			for _, id := range runIDs {
				s := states[id]
				if s != "succeeded" {
					return 1
				}
			}
			return 0
		}

		time.Sleep(pollInterval)
	}
}
