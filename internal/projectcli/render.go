package projectcli

import (
	"fmt"
	"io"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

// WriteProjectInfo renders the full "cloche project" output to w.
func WriteProjectInfo(resp *pb.GetProjectInfoResponse, w io.Writer) {
	fmt.Fprintf(w, "Project:     %s\n", resp.Name)
	fmt.Fprintf(w, "Directory:   %s\n", resp.ProjectDir)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Config:")
	fmt.Fprintf(w, "  active:             %v\n", resp.Active)
	fmt.Fprintf(w, "  concurrency:        %d\n", resp.Concurrency)
	if resp.StaggerSeconds > 0 {
		fmt.Fprintf(w, "  stagger_seconds:    %.1f\n", resp.StaggerSeconds)
	}
	if resp.DedupSeconds > 0 {
		fmt.Fprintf(w, "  dedup_seconds:      %.0f\n", resp.DedupSeconds)
	}
	fmt.Fprintf(w, "  stop_on_error:      %v\n", resp.StopOnError)
	fmt.Fprintf(w, "  max_consecutive_failures: %d\n", resp.MaxConsecutiveFailures)
	fmt.Fprintf(w, "  evolution:          %v\n", resp.EvolutionEnabled)
	fmt.Fprintln(w)

	loopState := "stopped"
	if resp.LoopRunning {
		loopState = "running"
	}
	fmt.Fprintf(w, "Loop:        %s\n", loopState)
	fmt.Fprintln(w)

	if len(resp.ActiveRuns) > 0 {
		fmt.Fprintf(w, "Active runs: %d\n", len(resp.ActiveRuns))
		for _, run := range resp.ActiveRuns {
			runType := "container"
			if run.IsHost {
				runType = "host"
			}
			line := fmt.Sprintf("  %s  %-20s  %-10s  %s", run.RunId, run.WorkflowName, run.State, runType)
			if run.Title != "" {
				t := run.Title
				if len(t) > 40 {
					t = t[:37] + "..."
				}
				line += "  " + t
			}
			fmt.Fprintln(w, line)
		}
	} else {
		fmt.Fprintln(w, "Active runs: none")
	}
	fmt.Fprintln(w)

	if len(resp.ContainerWorkflows) > 0 {
		fmt.Fprintln(w, "Container workflows:")
		for _, wfName := range resp.ContainerWorkflows {
			fmt.Fprintf(w, "  %s\n", wfName)
		}
	} else {
		fmt.Fprintln(w, "Container workflows: none")
	}

	if len(resp.HostWorkflows) > 0 {
		fmt.Fprintln(w, "Host workflows:")
		for _, wfName := range resp.HostWorkflows {
			fmt.Fprintf(w, "  %s\n", wfName)
		}
	} else {
		fmt.Fprintln(w, "Host workflows: none")
	}

	if len(resp.Repositories) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Repositories:")
		for _, repo := range resp.Repositories {
			def := ""
			if repo.Default {
				def = "  (default)"
			}
			if repo.Url != "" {
				fmt.Fprintf(w, "  %-20s  %-30s  %s%s\n", repo.Name, repo.Path, repo.Url, def)
			} else {
				fmt.Fprintf(w, "  %-20s  %s%s\n", repo.Name, repo.Path, def)
			}
		}
	}
}

// WriteReposList renders the "cloche project repos list" table to w.
func WriteReposList(repos []*pb.Repository, w io.Writer) {
	if len(repos) == 0 {
		fmt.Fprintln(w, "No repositories configured.")
		return
	}
	fmt.Fprintf(w, "%-20s  %-30s  %s\n", "NAME", "PATH", "FLAGS")
	for _, repo := range repos {
		flags := ""
		if repo.Default {
			flags = "default"
		}
		if repo.Url != "" {
			fmt.Fprintf(w, "%-20s  %-30s  %s  %s\n", repo.Name, repo.Path, repo.Url, flags)
		} else {
			fmt.Fprintf(w, "%-20s  %-30s  %s\n", repo.Name, repo.Path, flags)
		}
	}
}
