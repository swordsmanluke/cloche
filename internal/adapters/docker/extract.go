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

// ExtractOptions configures a call to ExtractResults.
type ExtractOptions struct {
	ContainerID  string
	ProjectDir   string
	RunID        string
	BaseSHA      string
	WorkflowName string
	Result       string
}

// ExtractResult holds the output of a successful ExtractResults call.
type ExtractResult struct {
	TargetDir string
	Branch    string
	CommitSHA string
}

// ExtractResults copies the container workspace to a git branch using worktrees.
// It creates branch cloche/<runID> based on baseSHA, copies the container's
// /workspace into the worktree's cloche/ directory, commits, and cleans up.
func ExtractResults(ctx context.Context, opts ExtractOptions) (ExtractResult, error) {
	if opts.BaseSHA == "" {
		return ExtractResult{}, fmt.Errorf("no base SHA recorded for run %s", opts.RunID)
	}

	// 1. Copy container workspace to temp dir
	tmpDir, err := os.MkdirTemp("", "cloche-extract-"+opts.RunID+"-")
	if err != nil {
		return ExtractResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cpCmd := exec.CommandContext(ctx, "docker", "cp", opts.ContainerID+":/workspace/.", tmpDir+"/")
	var cpStderr bytes.Buffer
	cpCmd.Stderr = &cpStderr
	if err := cpCmd.Run(); err != nil {
		return ExtractResult{}, fmt.Errorf("docker cp from container: %s: %w", cpStderr.String(), err)
	}

	// 2. Create git worktree
	worktreeDir := filepath.Join(opts.ProjectDir, ".gitworktrees", "cloche", opts.RunID)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return ExtractResult{}, fmt.Errorf("creating worktree parent: %w", err)
	}

	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", worktreeDir, opts.BaseSHA)
	addCmd.Dir = opts.ProjectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	defer func() {
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreeDir)
		rmCmd.Dir = opts.ProjectDir
		rmCmd.Run()
	}()

	// 3. Replace worktree contents with the container workspace.
	// First extract commit messages from the container's git history (before
	// removing .git), then remove existing files (preserving .git) and copy
	// from the container. This ensures file deletions by the agent are captured.
	containerCommits := extractContainerCommits(ctx, tmpDir, opts.BaseSHA)
	os.RemoveAll(filepath.Join(tmpDir, ".git"))
	entries, _ := os.ReadDir(worktreeDir)
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		os.RemoveAll(filepath.Join(worktreeDir, e.Name()))
	}
	cpLocalCmd := exec.CommandContext(ctx, "cp", "-a", tmpDir+"/.", worktreeDir+"/")
	if out, err := cpLocalCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("copying to worktree: %s: %w", out, err)
	}

	// 4. Create branch, add, commit
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)

	branch := "cloche/" + opts.RunID

	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	checkoutCmd.Dir = worktreeDir
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	addFilesCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addFilesCmd.Dir = worktreeDir
	addFilesCmd.Env = gitEnv
	if out, err := addFilesCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git add: %s: %w", out, err)
	}

	commitMsg := buildCommitMessage(ctx, worktreeDir, gitEnv, opts.RunID, opts.WorkflowName, opts.Result, containerCommits)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-F", "-", "--allow-empty")
	commitCmd.Dir = worktreeDir
	commitCmd.Env = gitEnv
	commitCmd.Stdin = strings.NewReader(commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git commit: %s: %w", out, err)
	}

	// 5. Capture the resulting commit SHA.
	revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revCmd.Dir = worktreeDir
	revOut, err := revCmd.Output()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commitSHA := strings.TrimSpace(string(revOut))

	return ExtractResult{
		TargetDir: worktreeDir,
		Branch:    branch,
		CommitSHA: commitSHA,
	}, nil
}

// buildCommitMessage generates a squash-style commit message. The title
// summarizes the workflow result. If the agent made commits inside the
// container, their messages are included (like git merge --squash). Otherwise
// falls back to a file-change summary.
func buildCommitMessage(ctx context.Context, worktreeDir string, gitEnv []string, runID, workflowName, result string, containerCommits string) string {
	title := fmt.Sprintf("cloche run %s: %s (%s)", runID, workflowName, result)

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

	// Include container commit messages (squash-style) if available.
	if containerCommits != "" {
		body.WriteString("Squashed commits:\n\n")
		body.WriteString(containerCommits)
	} else {
		// No container commits — fall back to file-change summary.
		nsCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-status")
		nsCmd.Dir = worktreeDir
		nsCmd.Env = gitEnv
		nsOut, err := nsCmd.Output()
		if err == nil && len(bytes.TrimSpace(nsOut)) > 0 {
			added, modified, deleted, renamed := classifyChanges(string(nsOut))
			writeChangeSection(&body, "Added", added)
			writeChangeSection(&body, "Modified", modified)
			writeChangeSection(&body, "Deleted", deleted)
			writeChangeSection(&body, "Renamed", renamed)
		}
	}

	if body.Len() == 0 {
		return title
	}

	return title + "\n\n" + strings.TrimRight(body.String(), "\n")
}

// extractContainerCommits reads commit messages from the container's git history
// since baseSHA. Returns a formatted string with each commit's message, suitable
// for inclusion in a squash commit. Returns "" if no commits were made or on error.
func extractContainerCommits(ctx context.Context, containerRepoDir, baseSHA string) string {
	// Use %x00 as delimiter between commits to handle multi-line messages.
	logCmd := exec.CommandContext(ctx, "git", "log", "--reverse", "--format=%B%x00", baseSHA+"..HEAD")
	logCmd.Dir = containerRepoDir
	out, err := logCmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return ""
	}

	raw := strings.TrimSpace(string(out))
	commits := strings.Split(raw, "\x00")

	var result strings.Builder
	for _, msg := range commits {
		msg = strings.TrimSpace(msg)
		if msg == "" {
			continue
		}
		// Indent each commit message and separate with blank lines.
		lines := strings.Split(msg, "\n")
		for i, line := range lines {
			if i == 0 {
				result.WriteString("  * ")
			} else {
				result.WriteString("    ")
			}
			result.WriteString(line)
			result.WriteString("\n")
		}
		result.WriteString("\n")
	}

	return strings.TrimRight(result.String(), "\n")
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
