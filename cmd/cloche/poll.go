package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

func cmdPoll(client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche poll <run-id>\n")
		os.Exit(1)
	}

	runID := args[0]
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
