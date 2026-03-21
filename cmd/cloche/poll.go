package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

func cmdPoll(client pb.ClocheServiceClient, args []string) {
	// Filter out --no-color (handled globally) and collect IDs.
	var ids []string
	for _, arg := range args {
		if arg == "--no-color" {
			continue
		}
		ids = append(ids, arg)
	}

	if len(ids) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche poll <id> [id...]\n")
		fmt.Fprintf(os.Stderr, "  <id> may be a task ID, attempt ID, workflow ID (attempt:workflow),\n")
		fmt.Fprintf(os.Stderr, "  or step ID (attempt:workflow:step)\n")
		os.Exit(1)
	}

	if len(ids) == 1 {
		cmdPollSingle(client, ids[0])
	} else {
		exitCode := cmdPollMulti(client, ids, os.Stdout, os.Stderr)
		os.Exit(exitCode)
	}
}

// extractStepName returns the step name from a step-level ID (3 colon-separated
// parts), or an empty string if the ID does not specify a step.
func extractStepName(id string) string {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return ""
}

func cmdPollSingle(client pb.ClocheServiceClient, id string) {
	pollInterval := 2 * time.Second
	containerDeadThreshold := 1 * time.Minute

	var lastStepCount int
	var lastState string

	// Detect step-level ID: 3 colon-separated parts → last part is step name.
	stepName := extractStepName(id)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{Id: id})
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
				fmt.Printf("[%s] Step %q completed: %s\n", ts, exec.StepName, colorStatus(exec.Result))
			}
		}
		lastStepCount = len(resp.StepExecutions)

		// When polling a specific step, exit as soon as that step completes.
		if stepName != "" {
			for _, exec := range resp.StepExecutions {
				if exec.StepName == stepName && exec.Result != "" {
					os.Exit(0)
				}
			}
		}

		// Print state changes
		if resp.State != lastState {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("[%s] Run %s is %s\n", ts, colorID(resp.RunId), colorStatus(resp.State))
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

// cmdPollMulti polls multiple IDs and displays a compact status summary.
// Returns 0 if all runs succeeded, 1 if any failed or were cancelled.
func cmdPollMulti(client pb.ClocheServiceClient, ids []string, stdout, stderr io.Writer) int {
	pollInterval := 2 * time.Second
	states := make(map[string]string, len(ids))

	for {
		changed := false
		for _, id := range ids {
			if isTerminalState(states[id]) {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{Id: id})
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
			for _, id := range ids {
				fmt.Fprintf(stdout, "%s: %s\n", colorID(id), colorStatus(states[id]))
			}
			fmt.Fprintln(stdout)
		}

		// Check if all runs are in terminal states
		allDone := true
		for _, id := range ids {
			if !isTerminalState(states[id]) && states[id] != "error" {
				allDone = false
				break
			}
		}

		if allDone {
			for _, id := range ids {
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
