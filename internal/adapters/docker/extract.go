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

// PrepareOptions controls PrepareExtractWorktree.
type PrepareOptions struct {
	ProjectDir string
	BaseSHA    string
	TargetDir  string
	Branch     string
}

// ExtractWorktree is the result of a successful PrepareExtractWorktree call.
// Callers pass it (by its fields) into ExtractResults to run the extraction
// into the prepared worktree, and are responsible for its eventual teardown.
type ExtractWorktree struct {
	Dir    string
	Branch string
}

// ExtractOptions controls ExtractResults.
//
// In git mode (NoGit = false), WorktreeDir must point at a worktree that was
// previously prepared by PrepareExtractWorktree (or any equivalent worktree
// checked out at BaseSHA on the Branch). Branch is used only for attribution
// in the commit message — the checkout state of the worktree is what controls
// which branch the commit lands on.
//
// In NoGit mode, the extraction is a plain docker cp into TargetDir and no
// git operations happen.
type ExtractOptions struct {
	ContainerID  string
	WorktreeDir  string
	Branch       string
	BaseSHA      string
	RunID        string
	WorkflowName string
	Result       string

	// ContainerSubPath, when non-empty, restricts extraction to a subdirectory
	// of the container's /workspace. Used by multi-repo projects to extract
	// each repository into its own worktree. Empty = extract the whole
	// /workspace (single-repo or legacy behavior).
	ContainerSubPath string

	// AuthorName / AuthorEmail configure the identity used for the extraction
	// commit. Empty strings fall back to the built-in "cloche <cloche@local>".
	AuthorName  string
	AuthorEmail string

	TargetDir string
	NoGit     bool
}

// containerWorkspacePath returns the absolute container path for a sub. Empty
// sub or "." resolves to /workspace.
func containerWorkspacePath(sub string) string {
	sub = strings.Trim(sub, "/")
	if sub == "" || sub == "." {
		return "/workspace"
	}
	return "/workspace/" + sub
}

const (
	defaultExtractAuthorName  = "cloche"
	defaultExtractAuthorEmail = "cloche@local"
)

// ExtractResult contains the outcome of a successful ExtractResults call.
type ExtractResult struct {
	TargetDir string
	Branch    string
	CommitSHA string
}

// dockerCp is a package-private hook so tests can override docker cp with
// a local fixture copy without requiring a real Docker daemon.
var dockerCp = func(ctx context.Context, src, dst string) error {
	return exec.CommandContext(ctx, "docker", "cp", src, dst).Run()
}

// dockerExec is a package-private hook so tests can override docker exec
// invocations (used to read container git history without a host-side copy).
var dockerExec = func(ctx context.Context, containerID string, cmd ...string) ([]byte, error) {
	args := append([]string{"exec", containerID}, cmd...)
	return exec.CommandContext(ctx, "docker", args...).Output()
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

// PrepareExtractWorktree creates a git worktree at opts.TargetDir checked out
// on a new branch opts.Branch, branched from opts.BaseSHA. The caller owns the
// lifecycle — teardown (git worktree remove + branch -D) is not performed here.
func PrepareExtractWorktree(ctx context.Context, opts PrepareOptions) (ExtractWorktree, error) {
	if opts.ProjectDir == "" {
		return ExtractWorktree{}, fmt.Errorf("ProjectDir is required")
	}
	if opts.BaseSHA == "" {
		return ExtractWorktree{}, fmt.Errorf("BaseSHA is required")
	}
	if opts.TargetDir == "" {
		return ExtractWorktree{}, fmt.Errorf("TargetDir is required")
	}
	if opts.Branch == "" {
		return ExtractWorktree{}, fmt.Errorf("Branch is required")
	}

	if err := checkTargetEmpty(opts.TargetDir); err != nil {
		return ExtractWorktree{}, err
	}
	if err := checkBranchNotExists(ctx, opts.ProjectDir, opts.Branch); err != nil {
		return ExtractWorktree{}, err
	}

	if err := os.MkdirAll(filepath.Dir(opts.TargetDir), 0755); err != nil {
		return ExtractWorktree{}, fmt.Errorf("creating worktree parent: %w", err)
	}

	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", opts.Branch, opts.TargetDir, opts.BaseSHA)
	addCmd.Dir = opts.ProjectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return ExtractWorktree{}, fmt.Errorf("git worktree add: %s: %w", out, err)
	}

	return ExtractWorktree{Dir: opts.TargetDir, Branch: opts.Branch}, nil
}

// ExtractResults copies the container workspace into a pre-existing worktree
// and commits it (git mode), or copies it into a plain target directory
// (NoGit mode).
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
	if err := checkTargetEmpty(opts.TargetDir); err != nil {
		return ExtractResult{}, err
	}
	if err := os.MkdirAll(opts.TargetDir, 0755); err != nil {
		return ExtractResult{}, fmt.Errorf("creating target dir: %w", err)
	}
	src := opts.ContainerID + ":" + containerWorkspacePath(opts.ContainerSubPath) + "/."
	if err := dockerCp(ctx, src, opts.TargetDir+"/"); err != nil {
		return ExtractResult{}, fmt.Errorf("docker cp from container: %w", err)
	}
	return ExtractResult{TargetDir: opts.TargetDir}, nil
}

func extractGit(ctx context.Context, opts ExtractOptions) (ExtractResult, error) {
	if opts.WorktreeDir == "" {
		return ExtractResult{}, fmt.Errorf("WorktreeDir is required in git mode")
	}
	if opts.BaseSHA == "" {
		return ExtractResult{}, fmt.Errorf("no base SHA recorded for run %s", opts.RunID)
	}

	gitPointerPath := filepath.Join(opts.WorktreeDir, ".git")
	gitPointer, err := os.ReadFile(gitPointerPath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("reading worktree .git pointer: %w", err)
	}

	containerCommits := containerCommitsFromDocker(ctx, opts.ContainerID, opts.BaseSHA, opts.ContainerSubPath)

	entries, err := os.ReadDir(opts.WorktreeDir)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("reading worktree dir: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(opts.WorktreeDir, e.Name())); err != nil {
			return ExtractResult{}, fmt.Errorf("wiping worktree entry %q: %w", e.Name(), err)
		}
	}

	cpSrc := opts.ContainerID + ":" + containerWorkspacePath(opts.ContainerSubPath) + "/."
	if err := dockerCp(ctx, cpSrc, opts.WorktreeDir+"/"); err != nil {
		return ExtractResult{}, fmt.Errorf("docker cp from container: %w", err)
	}

	// docker cp may have landed a .git (file or dir) from the container on top
	// of the worktree. Remove it and restore the worktree pointer.
	if err := os.RemoveAll(gitPointerPath); err != nil {
		return ExtractResult{}, fmt.Errorf("removing copied .git: %w", err)
	}
	if err := os.WriteFile(gitPointerPath, gitPointer, 0644); err != nil {
		return ExtractResult{}, fmt.Errorf("restoring worktree .git pointer: %w", err)
	}

	name := opts.AuthorName
	if name == "" {
		name = defaultExtractAuthorName
	}
	email := opts.AuthorEmail
	if email == "" {
		email = defaultExtractAuthorEmail
	}
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email,
	)

	addFilesCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addFilesCmd.Dir = opts.WorktreeDir
	addFilesCmd.Env = gitEnv
	if out, err := addFilesCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git add: %s: %w", out, err)
	}

	// Sanity-gate the staged commit. If extraction would mass-delete files
	// without producing comparable additions, the underlying container almost
	// certainly didn't have a complete project copy (see bug cloche-9d59).
	// Committing in that case overwrites the worktree branch with a wholesale
	// "delete most of the project" commit, poisoning subsequent runs that read
	// from the same branch. Refuse to commit and surface a clear error so the
	// caller can decide what to do (typically: log it and fail the run).
	if dels, adds, err := stagedFileCounts(ctx, opts.WorktreeDir); err == nil {
		if dels > extractMaxDeletions && dels > adds*2 {
			return ExtractResult{}, fmt.Errorf(
				"extraction would delete %d files (with only %d additions); refusing to commit. "+
					"This usually means the container's /workspace was incomplete — check the project copy. "+
					"Override with CLOCHE_EXTRACT_ALLOW_MASS_DELETE=1 if the deletions are intentional.",
				dels, adds)
		}
	}

	commitMsg := buildCommitMessage(ctx, opts.WorktreeDir, gitEnv, opts.RunID, opts.WorkflowName, opts.Result, containerCommits)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-F", "-", "--allow-empty")
	commitCmd.Dir = opts.WorktreeDir
	commitCmd.Env = gitEnv
	commitCmd.Stdin = strings.NewReader(commitMsg)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return ExtractResult{}, fmt.Errorf("git commit: %s: %w", out, err)
	}

	revCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revCmd.Dir = opts.WorktreeDir
	revOut, err := revCmd.Output()
	if err != nil {
		return ExtractResult{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commitSHA := strings.TrimSpace(string(revOut))

	return ExtractResult{
		TargetDir: opts.WorktreeDir,
		Branch:    opts.Branch,
		CommitSHA: commitSHA,
	}, nil
}

// extractMaxDeletions is the threshold above which the extraction sanity gate
// kicks in. Combined with the deletions > additions*2 ratio check, this
// catches "container was incomplete" cases without flagging legitimate large
// refactors. Tuned for cloche projects where a typical run touches at most a
// few dozen files.
const extractMaxDeletions = 50

// stagedFileCounts returns the number of staged additions and deletions in the
// given worktree. Used by the extraction sanity gate to detect bad project
// copies before they get committed (see ExtractResults).
//
// Returns 0/0 with no error if `git diff --cached --diff-filter=...` fails
// for any reason — in that case the caller falls through to the original
// behavior rather than blocking on a transient git failure.
func stagedFileCounts(ctx context.Context, worktreeDir string) (deletions, additions int, err error) {
	if os.Getenv("CLOCHE_EXTRACT_ALLOW_MASS_DELETE") == "1" {
		// Operator opt-out for legitimate mass-delete refactors.
		return 0, 0, nil
	}
	count := func(filter string) (int, error) {
		cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--diff-filter="+filter, "--name-only")
		cmd.Dir = worktreeDir
		out, err := cmd.Output()
		if err != nil {
			return 0, err
		}
		n := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		return n, nil
	}
	dels, err := count("D")
	if err != nil {
		return 0, 0, err
	}
	adds, err := count("A")
	if err != nil {
		return 0, 0, err
	}
	return dels, adds, nil
}

// buildCommitMessage generates a squash-style commit message. The title
// prefers the agent's most recent commit subject — that line was written by
// the in-container agent and typically describes what the run actually
// produced ("Add BDD test plan for…", "Wire repository store into project
// loader") rather than the bookkeeping prefix. Falls back to a generic
// "cloche run X: Y" framing when no agent commits are present.
//
// The body always carries the file-change summary plus the full list of
// agent commit messages (like `git merge --squash` would), so reviewers can
// still see every step's wording.
func buildCommitMessage(ctx context.Context, worktreeDir string, gitEnv []string, runID, workflowName, result string, containerCommits string) string {
	bookkeeping := fmt.Sprintf("cloche run %s: %s (%s)", runID, workflowName, result)
	title := firstAgentCommitSubject(containerCommits)
	if title == "" {
		title = bookkeeping
	}

	statCmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--stat", "--stat-width=72")
	statCmd.Dir = worktreeDir
	statCmd.Env = gitEnv
	statOut, _ := statCmd.Output()
	statSummary := extractStatSummary(string(statOut))

	var body strings.Builder
	// Always include the bookkeeping line so the extracted commit still
	// identifies which run produced it, even when the title is taken from
	// the agent's commit.
	if title != bookkeeping {
		body.WriteString(bookkeeping)
		body.WriteString("\n\n")
	}
	if statSummary != "" {
		body.WriteString(statSummary)
		body.WriteString("\n\n")
	}

	if containerCommits != "" {
		body.WriteString("Squashed commits:\n\n")
		body.WriteString(containerCommits)
	} else {
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

// firstAgentCommitSubject returns the subject line of the most recent
// non-bookkeeping commit in `containerCommits` (the squashed-list string
// `containerCommitsFromDocker` produces). Bookkeeping subjects — `cloche run
// …` and `Version X.Y.Z` lines that ride along from prior workflow auto-
// commits — are skipped so the chosen subject describes the actual work the
// agent did. Returns "" if no qualifying subject exists.
func firstAgentCommitSubject(containerCommits string) string {
	if containerCommits == "" {
		return ""
	}
	var lastAgentSubject string
	for _, raw := range strings.Split(containerCommits, "\n") {
		line := strings.TrimRight(raw, " ")
		// Only consider subject lines (those that start with "  * ").
		const prefix = "  * "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		subj := strings.TrimSpace(line[len(prefix):])
		if subj == "" {
			continue
		}
		if strings.HasPrefix(subj, "cloche run ") || strings.HasPrefix(subj, "Version ") {
			continue
		}
		lastAgentSubject = subj
	}
	return lastAgentSubject
}

// containerCommitsFromDocker reads commit messages from the container's git
// history since baseSHA, using `docker exec git log`. Returns a formatted
// string with each commit's message, suitable for inclusion in a squash
// commit. Returns "" if no commits were made or on error.
//
// containerSubPath, when non-empty, scopes the log to a subdirectory of
// /workspace (used for multi-repo projects where each repo has its own git
// history under /workspace/<sub>).
func containerCommitsFromDocker(ctx context.Context, containerID, baseSHA, containerSubPath string) string {
	out, err := dockerExec(ctx, containerID,
		"git", "-C", containerWorkspacePath(containerSubPath), "log", "--reverse", "--format=%B%x00", baseSHA+"..HEAD")
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
