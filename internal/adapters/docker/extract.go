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

// ExtractOptions controls how ExtractResults behaves.
// Zero values for the new fields preserve the original behavior.
type ExtractOptions struct {
	ContainerID  string
	ProjectDir   string
	RunID        string
	BaseSHA      string
	WorkflowName string
	Result       string

	// New fields — zero values preserve today's behavior.
	TargetDir string // "" → <ProjectDir>/.gitworktrees/cloche/<RunID>
	Branch    string // "" → cloche/<RunID>
	NoGit     bool   // skip worktree/branch/commit; only docker cp
	Persist   bool   // skip the defer-remove of the worktree
}

// ExtractResult contains the outcome of a successful ExtractResults call.
type ExtractResult struct {
	TargetDir string
	Branch    string // empty when NoGit
	CommitSHA string // empty when NoGit
}

// dockerCp is a package-private hook so tests can override docker cp with
// a local fixture copy without requiring a real Docker daemon.
var dockerCp = func(ctx context.Context, src, dst string) error {
	return exec.CommandContext(ctx, "docker", "cp", src, dst).Run()
}

// checkTargetEmpty returns an error if dir exists and is non-empty.
func checkTargetEmpty(dir string) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target %q exists and is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("readdir %q: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("target %q is not empty", dir)
	}
	return nil
}

// checkBranchNotExists returns an error if branch already exists in the repo.
func checkBranchNotExists(ctx context.Context, projectDir, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "refs/heads/"+branch)
	cmd.Dir = projectDir
	if err := cmd.Run(); err == nil {
		return fmt.Errorf("branch %q already exists", branch)
	}
	return nil
}

// ExtractResults copies the container workspace to a git branch using worktrees,
// or to a plain directory when NoGit is true. Existing callers pass only the
// original six fields; zero values for the new fields preserve today's behavior.
func ExtractResults(ctx context.Context, opts ExtractOptions) (ExtractResult, error) {
	if opts.NoGit {
		return extractNoGit(ctx, opts)
	}
	return extractGit(ctx, opts)
}

func extractNoGit(ctx context.Context, opts ExtractOptions) (ExtractResult, error) {
	if opts.TargetDir == "" {
		return ExtractResult{}, fmt.Errorf("TargetDir is required when NoGit is true")
	}
	// Pre-check: target must be nonexistent or empty.
	if err := checkTargetEmpty(opts.TargetDir); err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(opts.TargetDir, 0755); err != nil {
		return ExtractResult{}, fmt.Errorf("creating target dir: %w", err)
	}
	if err := dockerCp(ctx, opts.ContainerID+":/workspace/.", opts.TargetDir+"/"); err != nil {
		return ExtractResult{}, fmt.Errorf("docker cp from container: %w", err)
	}
	return ExtractResult{TargetDir: opts.TargetDir}, nil
}

func extractGit(ctx context.Context, opts ExtractOptions) (ExtractResult, error) {
	if opts.BaseSHA == "" {
		return ExtractResult{}, fmt.Errorf("no base SHA recorded for run %s", opts.RunID)
	}

	// Resolve target dir and branch, falling back to defaults.
	targetDir := opts.TargetDir
	if targetDir == "" {
		targetDir = filepath.Join(opts.ProjectDir, ".gitworktrees", "cloche", opts.RunID)
	}
	branch := opts.Branch
	if branch == "" {
		branch = "cloche/" + opts.RunID
	}

	// Pre-checks (only when fields are explicitly set).
	if opts.TargetDir != "" {
		if err := checkTargetEmpty(opts.TargetDir); err != nil {
			return ExtractResult{}, err
		}
	}
	if opts.Branch != "" {
		if err := checkBranchNotExists(ctx, opts.ProjectDir, opts.Branch); err != nil {
			return ExtractResult{}, err
		}
	}

	// 1. Copy container workspace to temp dir
	tmpDir, err := os.MkdirTemp("", "cloche-extract-"+opts.RunID+"-")
	if err != nil {
		return ExtractResult{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := dockerCp(ctx, opts.ContainerID+":/workspace/.", tmpDir+"/"); err != nil {
		return ExtractResult{}, fmt.Errorf("docker cp from container: %w", err)
	}

	// 2. Create git worktree
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return ExtractResult{}, fmt.Errorf("creating worktree parent: %w", err)
	}

	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", targetDir, opts.BaseSHA)
	addCmd.Dir = opts.ProjectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	if !opts.Persist {
		defer func() {
			rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", targetDir)
			rmCmd.Dir = opts.ProjectDir
			rmCmd.Run()
		}()
	}

	// 3. Replace worktree contents with the container workspace.
	// Extract commit messages from container git history before removing .git.
	containerCommits := extractContainerCommits(ctx, tmpDir, opts.BaseSHA)
	os.RemoveAll(filepath.Join(tmpDir, ".git"))
	entries, _ := os.ReadDir(targetDir)
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		os.RemoveAll(filepath.Join(targetDir, e.Name()))
	}
	cpLocalCmd := exec.CommandContext(ctx, "cp", "-a", tmpDir+"/.", targetDir+"/")
	if out, err := cpLocalCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("copying to worktree: %s: %w", out, err)
	}

	// 4. Create branch, add, commit.
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)

	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	checkoutCmd.Dir = targetDir
	checkoutCmd.Env = gitEnv
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	addFilesCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addFilesCmd.Dir = targetDir
	addFilesCmd.Env = gitEnv
	if out, err := addFilesCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git add: %s: %w", out, err)
	}

	commitMsg := buildCommitMessage(ctx, targetDir, gitEnv, opts.RunID, opts.WorkflowName, opts.Result, containerCommits)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-F", "-", "--allow-empty")
	commitCmd.Dir = targetDir
	commitCmd.Env = gitEnv
	commitCmd.Stdin = strings.NewReader(commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git commit: %s: %w", out, err)
	}

	// Get the commit SHA.
	revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revCmd.Dir = targetDir
	revOut, err := revCmd.Output()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commitSHA := strings.TrimSpace(string(revOut))

	return ExtractResult{
		TargetDir: targetDir,
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
