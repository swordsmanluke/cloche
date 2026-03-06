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

// MergeAgent merges feature branches into main using rebase-first strategy.
// On conflict, it prefers main's version (-X ours from the feature branch's
// perspective). If rebase fails entirely, the merge is flagged as failed and
// the branch is preserved for human review.
type MergeAgent struct {
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

// Merge takes a merge queue entry and rebases its branch onto main, then
// fast-forwards main to the rebased HEAD.
//
// Strategy:
//  1. Create a worktree checked out at the feature branch
//  2. Rebase onto main with -X ours (prefer main on conflict)
//  3. On success: update-ref main to rebased HEAD, delete branch
//  4. On failure: abort rebase, flag as failed, preserve branch
func (m *MergeAgent) Merge(ctx context.Context, entry *ports.MergeQueueEntry, projectDir string) error {
	worktreeDir := filepath.Join(projectDir, ".gitworktrees", "merge", entry.RunID)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	// Create worktree at the feature branch
	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", worktreeDir, entry.Branch)
	addCmd.Dir = projectDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s: %w", out, err)
	}

	removeWorktree := func() {
		rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreeDir)
		rmCmd.Dir = projectDir
		rmCmd.Run()
	}

	env := gitEnv()

	// Rebase the feature branch onto main, preferring main on conflict
	rebaseCmd := exec.CommandContext(ctx, "git", "rebase", "-X", "ours", "main")
	rebaseCmd.Dir = worktreeDir
	rebaseCmd.Env = env
	if out, err := rebaseCmd.CombinedOutput(); err != nil {
		// Rebase failed — abort and preserve branch for human review
		abortRebase(ctx, worktreeDir)
		removeWorktree()
		return fmt.Errorf("rebase failed (branch preserved for review): %s: %w", out, err)
	}

	// Run validation if configured
	if m.Validate != "" {
		validateCmd := exec.CommandContext(ctx, "sh", "-c", m.Validate)
		validateCmd.Dir = worktreeDir
		validateCmd.Env = env
		if out, err := validateCmd.CombinedOutput(); err != nil {
			removeWorktree()
			return fmt.Errorf("validation failed: %s: %w", out, err)
		}
	}

	// Fast-forward main to the rebased HEAD
	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	headCmd.Dir = worktreeDir
	rebasedHead, err := headCmd.Output()
	if err != nil {
		removeWorktree()
		return fmt.Errorf("rev-parse HEAD: %w", err)
	}

	// Must remove worktree before updating main ref and deleting branch,
	// because git won't allow deleting a branch checked out in a worktree.
	removeWorktree()

	updateCmd := exec.CommandContext(ctx, "git", "update-ref", "refs/heads/main",
		string(bytes.TrimSpace(rebasedHead)))
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

// abortRebase runs git rebase --abort to clean up a failed rebase.
func abortRebase(ctx context.Context, worktreeDir string) {
	cmd := exec.CommandContext(ctx, "git", "rebase", "--abort")
	cmd.Dir = worktreeDir
	cmd.Run()
}
