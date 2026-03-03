package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	commitMsg := fmt.Sprintf("cloche run %s: %s (%s)", runID, workflowName, result)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg, "--allow-empty")
	commitCmd.Dir = worktreeDir
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", out, err)
	}

	return nil
}
