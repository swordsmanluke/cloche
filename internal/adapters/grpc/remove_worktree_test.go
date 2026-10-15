package grpc

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/swordsmanluke/cloche/internal/adapters/docker"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func TestRemoveExtractWorktree_KeepsBranchUnlessAsked(t *testing.T) {
	for _, del := range []bool{false, true} {
		repo := t.TempDir()
		gitRun(t, repo, "init", "-q")
		gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
		sha := gitRun(t, repo, "rev-parse", "HEAD")
		wt, err := docker.PrepareExtractWorktree(context.Background(), docker.PrepareOptions{
			ProjectDir: repo, BaseSHA: sha[:len(sha)-1],
			TargetDir: filepath.Join(repo, ".gitworktrees", "cloche", "r1"), Branch: "cloche/r1",
		})
		if err != nil {
			t.Fatal(err)
		}
		removeExtractWorktree(context.Background(), repo, wt, del)
		err = exec.Command("git", "-C", repo, "rev-parse", "--verify", "refs/heads/cloche/r1").Run()
		if del && err == nil {
			t.Error("branch should be deleted when deleteBranch=true")
		}
		if !del && err != nil {
			t.Error("branch must survive worktree removal when deleteBranch=false")
		}
	}
}
