package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/ports"
)

// MergeAgent merges feature branches into main using git worktrees.
// If merge conflicts arise, it calls an LLM to resolve them.
type MergeAgent struct {
	LLM        LLMClient
	MergeQueue ports.MergeQueueStore
	Validate   string // optional validation command (e.g. "make test")
}

// gitEnv returns environment variables for cloche git operations.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)
}

// Merge takes a merge queue entry and merges its branch into main.
// On success: completes the merge commit, removes worktree, deletes the branch.
// On failure: aborts the merge, removes worktree, preserves the branch.
func (m *MergeAgent) Merge(ctx context.Context, entry *ports.MergeQueueEntry, projectDir string) error {
	worktreeDir := filepath.Join(projectDir, ".gitworktrees", "merge", entry.RunID)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	// Create worktree at main (detached to avoid "already checked out" error)
	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", worktreeDir, "main")
	addCmd.Dir = projectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", out, err)
	}
	defer func() {
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreeDir)
		rmCmd.Dir = projectDir
		rmCmd.Run()
	}()

	env := gitEnv()

	// Merge the feature branch
	mergeCmd := exec.CommandContext(ctx, "git", "merge", entry.Branch, "--no-ff",
		"-m", fmt.Sprintf("Merge %s into main", entry.Branch))
	mergeCmd.Dir = worktreeDir
	mergeCmd.Env = env
	var mergeStderr bytes.Buffer
	mergeCmd.Stderr = &mergeStderr

	if err := mergeCmd.Run(); err != nil {
		// Check if this is a merge conflict
		conflictFiles, conflictErr := listConflicts(ctx, worktreeDir)
		if conflictErr != nil || len(conflictFiles) == 0 {
			// Not a conflict, just a merge failure
			abortMerge(ctx, worktreeDir)
			return fmt.Errorf("git merge failed: %s: %w", mergeStderr.String(), err)
		}

		// Try LLM-based conflict resolution
		if resolveErr := m.resolveConflicts(ctx, worktreeDir, entry.Branch, conflictFiles); resolveErr != nil {
			abortMerge(ctx, worktreeDir)
			return fmt.Errorf("conflict resolution failed: %w", resolveErr)
		}
	}

	// Run validation if configured
	if m.Validate != "" {
		validateCmd := exec.CommandContext(ctx, "sh", "-c", m.Validate)
		validateCmd.Dir = worktreeDir
		validateCmd.Env = env
		if out, err := validateCmd.CombinedOutput(); err != nil {
			// Reset the merge so worktree cleanup works
			resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", "main")
			resetCmd.Dir = worktreeDir
			resetCmd.Run()
			return fmt.Errorf("validation failed: %s: %w", out, err)
		}
	}

	// Push the merge to main: update main ref to the worktree HEAD
	updateRefCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	updateRefCmd.Dir = worktreeDir
	mergeCommit, err := updateRefCmd.Output()
	if err != nil {
		return fmt.Errorf("rev-parse HEAD: %w", err)
	}

	updateCmd := exec.CommandContext(ctx, "git", "update-ref", "refs/heads/main",
		string(bytes.TrimSpace(mergeCommit)))
	updateCmd.Dir = projectDir
	if out, err := updateCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update-ref main: %s: %w", out, err)
	}

	// Delete the feature branch
	delCmd := exec.CommandContext(ctx, "git", "branch", "-D", entry.Branch)
	delCmd.Dir = projectDir
	if out, err := delCmd.CombinedOutput(); err != nil {
		log.Printf("merge-agent: warning: failed to delete branch %s: %s: %v", entry.Branch, out, err)
	}

	return nil
}

// ProcessQueue dequeues the next pending merge and processes it.
// Returns true if a merge was processed, false if the queue was empty or busy.
func (m *MergeAgent) ProcessQueue(ctx context.Context, projectDir string) bool {
	entry, err := m.MergeQueue.NextPendingMerge(ctx, projectDir)
	if err != nil {
		log.Printf("merge-agent: failed to dequeue: %v", err)
		return false
	}
	if entry == nil {
		return false
	}

	log.Printf("merge-agent: processing merge for run %s (branch %s)", entry.RunID, entry.Branch)

	if err := m.Merge(ctx, entry, projectDir); err != nil {
		log.Printf("merge-agent: merge failed for run %s: %v", entry.RunID, err)
		_ = m.MergeQueue.FailMerge(ctx, entry.RunID)
		return true
	}

	log.Printf("merge-agent: merge succeeded for run %s", entry.RunID)
	_ = m.MergeQueue.CompleteMerge(ctx, entry.RunID)

	// Try to process the next entry in the queue
	m.ProcessQueue(ctx, projectDir)
	return true
}

// resolveConflicts asks the LLM to resolve merge conflicts.
func (m *MergeAgent) resolveConflicts(ctx context.Context, worktreeDir, branch string, conflictFiles []string) error {
	if m.LLM == nil {
		return fmt.Errorf("no LLM configured for conflict resolution")
	}

	systemPrompt := `You are a merge conflict resolver. You will receive git merge conflict markers in files.
For each file, output the resolved content — the entire file with conflicts resolved correctly.
Preserve the intent of both sides of the merge. Choose the most sensible combination.

Output format: For each file, output a line "=== FILE: <path> ===" followed by the resolved file content.
Do not include any other commentary.`

	// Build user prompt with conflict content
	var userParts []string
	userParts = append(userParts, fmt.Sprintf("Merging branch %s into main.\nConflicting files:\n", branch))

	for _, f := range conflictFiles {
		content, err := os.ReadFile(filepath.Join(worktreeDir, f))
		if err != nil {
			continue
		}
		userParts = append(userParts, fmt.Sprintf("=== FILE: %s ===\n%s\n", f, string(content)))
	}

	userPrompt := ""
	for _, p := range userParts {
		userPrompt += p
	}

	response, err := m.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("LLM conflict resolution: %w", err)
	}

	// Parse response and write resolved files
	if err := applyResolvedFiles(worktreeDir, response, conflictFiles); err != nil {
		return fmt.Errorf("applying resolved files: %w", err)
	}

	// Stage resolved files and complete the merge commit
	env := gitEnv()

	addCmd := exec.CommandContext(ctx, "git", "add", "-A")
	addCmd.Dir = worktreeDir
	addCmd.Env = env
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add after resolve: %s: %w", out, err)
	}

	commitCmd := exec.CommandContext(ctx, "git", "commit", "--no-edit")
	commitCmd.Dir = worktreeDir
	commitCmd.Env = env
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit after resolve: %s: %w", out, err)
	}

	return nil
}

// listConflicts returns the list of files with merge conflicts.
func listConflicts(ctx context.Context, worktreeDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = worktreeDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var files []string
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		if len(line) > 0 {
			files = append(files, string(line))
		}
	}
	return files, nil
}

// applyResolvedFiles parses LLM output and writes resolved file contents.
func applyResolvedFiles(worktreeDir, response string, conflictFiles []string) error {
	// Parse "=== FILE: <path> ===" sections from response
	sections := parseFileSections(response)

	if len(sections) == 0 {
		return fmt.Errorf("LLM response contained no file sections")
	}

	for path, content := range sections {
		fullPath := filepath.Join(worktreeDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing resolved %s: %w", path, err)
		}
	}

	return nil
}

// parseFileSections extracts file path -> content pairs from LLM output.
func parseFileSections(response string) map[string]string {
	sections := make(map[string]string)
	lines := bytes.Split([]byte(response), []byte("\n"))

	var currentFile string
	var content []byte

	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("=== FILE: ")) && bytes.HasSuffix(trimmed, []byte(" ===")) {
			// Save previous file
			if currentFile != "" {
				sections[currentFile] = string(bytes.TrimRight(content, "\n"))
			}

			// Extract file path
			path := string(trimmed[len("=== FILE: ") : len(trimmed)-len(" ===")])
			currentFile = path
			content = nil
		} else if currentFile != "" {
			content = append(content, line...)
			content = append(content, '\n')
		}
	}

	// Save last file
	if currentFile != "" {
		sections[currentFile] = string(bytes.TrimRight(content, "\n"))
	}

	return sections
}

// abortMerge runs git merge --abort to clean up a failed merge.
func abortMerge(ctx context.Context, worktreeDir string) {
	cmd := exec.CommandContext(ctx, "git", "merge", "--abort")
	cmd.Dir = worktreeDir
	cmd.Run()
}
