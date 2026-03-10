package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExtractResults copies the container workspace to a git branch using worktrees.
// It creates branch cloche/<runID> based on baseSHA, copies the container's
// /workspace into the worktree's cloche/ directory, commits, and cleans up.
func ExtractResults(ctx context.Context, containerID, projectDir, runID, baseSHA, workflowName, result string) error {
	if baseSHA == "" {
		return fmt.Errorf("no base SHA recorded for run %s", runID)
	}

	// 1. Copy container workspace to temp dir
	tmpDir, err := os.MkdirTemp("", "cloche-extract-"+runID+"-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cpCmd := exec.CommandContext(ctx, "docker", "cp", containerID+":/workspace/.", tmpDir+"/")
	var cpStderr bytes.Buffer
	cpCmd.Stderr = &cpStderr
	if err := cpCmd.Run(); err != nil {
		return fmt.Errorf("docker cp from container: %s: %w", cpStderr.String(), err)
	}

	// 2. Create git worktree
	worktreeDir := filepath.Join(projectDir, ".gitworktrees", "cloche", runID)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", worktreeDir, baseSHA)
	addCmd.Dir = projectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	defer func() {
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreeDir)
		rmCmd.Dir = projectDir
		rmCmd.Run()
	}()

	// 3. Copy temp dir contents into worktree root, excluding .git
	// The container includes .git/ as a full directory, but the worktree has a
	// .git file pointing back to the main repo. We must not overwrite it.
	os.RemoveAll(filepath.Join(tmpDir, ".git"))
	cpLocalCmd := exec.CommandContext(ctx, "cp", "-a", tmpDir+"/.", worktreeDir+"/")
	if out, err := cpLocalCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copying to worktree: %s: %w", out, err)
	}

	// 4. Create branch, add, commit
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)

	branch := "cloche/" + runID

	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	checkoutCmd.Dir = worktreeDir
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	addFilesCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addFilesCmd.Dir = worktreeDir
	addFilesCmd.Env = gitEnv
	if out, err := addFilesCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", out, err)
	}

	commitMsg := buildCommitMessage(ctx, worktreeDir, gitEnv, runID, workflowName, result)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-F", "-", "--allow-empty")
	commitCmd.Dir = worktreeDir
	commitCmd.Env = gitEnv
	commitCmd.Stdin = strings.NewReader(commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", out, err)
	}

	return nil
}

// buildCommitMessage generates a descriptive commit message by inspecting the
// staged diff in the worktree. The title summarizes the workflow result, and
// the body lists file-level changes grouped by operation (added, modified,
// deleted, renamed).
func buildCommitMessage(ctx context.Context, worktreeDir string, gitEnv []string, runID, workflowName, result string) string {
	title := fmt.Sprintf("cloche run %s: %s (%s)", runID, workflowName, result)

	// Get name-status of staged changes relative to parent
	nsCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-status")
	nsCmd.Dir = worktreeDir
	nsCmd.Env = gitEnv
	nsOut, err := nsCmd.Output()
	if err != nil || len(bytes.TrimSpace(nsOut)) == 0 {
		return title
	}

	added, modified, deleted, renamed := classifyChanges(string(nsOut))

	// Get diffstat summary line (e.g. "5 files changed, 100 insertions(+), 20 deletions(-)")
	statCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--stat", "--stat-width=72")
	statCmd.Dir = worktreeDir
	statCmd.Env = gitEnv
	statOut, _ := statCmd.Output()
	statSummary := extractStatSummary(string(statOut))

	var body strings.Builder
	if statSummary != "" {
		body.WriteString(statSummary)
		body.WriteString("\n\n")
	}
	writeChangeSection(&body, "Added", added)
	writeChangeSection(&body, "Modified", modified)
	writeChangeSection(&body, "Deleted", deleted)
	writeChangeSection(&body, "Renamed", renamed)

	if body.Len() == 0 {
		return title
	}

	return title + "\n\n" + strings.TrimRight(body.String(), "\n")
}

// classifyChanges parses git diff --name-status output into categorized file lists.
func classifyChanges(nameStatus string) (added, modified, deleted, renamed []string) {
	for _, line := range strings.Split(strings.TrimSpace(nameStatus), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		switch {
		case status == "A":
			added = append(added, fields[1])
		case status == "M":
			modified = append(modified, fields[1])
		case status == "D":
			deleted = append(deleted, fields[1])
		case strings.HasPrefix(status, "R"):
			if len(fields) >= 3 {
				renamed = append(renamed, fields[1]+" -> "+fields[2])
			}
		}
	}
	return
}

// extractStatSummary returns the last line of git diff --stat output, which
// contains the summary (e.g. "3 files changed, 10 insertions(+), 2 deletions(-)").
func extractStatSummary(stat string) string {
	lines := strings.Split(strings.TrimSpace(stat), "\n")
	if len(lines) == 0 {
		return ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.Contains(last, "changed") {
		return last
	}
	return ""
}

// writeChangeSection writes a labeled list of files to the builder if the list is non-empty.
func writeChangeSection(b *strings.Builder, label string, files []string) {
	if len(files) == 0 {
		return
	}
	b.WriteString(label)
	b.WriteString(":\n")
	for _, f := range files {
		b.WriteString("  ")
		b.WriteString(f)
		b.WriteString("\n")
	}
}
