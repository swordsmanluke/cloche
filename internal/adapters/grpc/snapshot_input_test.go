package grpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initSnapshotTestRepo creates a git repo in t.TempDir with two commits:
//
//	commit 1: A=v1
//	commit 2: A=v2, B=b
//
// then dirties the working tree (A=v3 on disk, untracked C). It returns the
// repo dir and the HEAD sha (the v2 commit).
func initSnapshotTestRepo(t *testing.T) (repoDir, headSHA string) {
	t.Helper()
	dir := t.TempDir()

	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test User")

	write("A", "v1")
	run("git", "add", "A")
	run("git", "commit", "-m", "A v1")

	write("A", "v2")
	write("B", "b")
	run("git", "add", "A", "B")
	run("git", "commit", "-m", "A v2 + B")

	headSHA = run("git", "rev-parse", "HEAD")

	// Dirty the working tree: A=v3 on disk, untracked C.
	write("A", "v3")
	write("C", "untracked")

	return dir, headSHA
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMaterializeCleanSnapshot_ExtractsTrackedFilesAtRef(t *testing.T) {
	repo, head := initSnapshotTestRepo(t)

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, head)
	if err != nil {
		t.Fatalf("materializeCleanSnapshot: %v", err)
	}

	// A must be the committed v2, NOT the dirty working-tree v3.
	if got := readFile(t, filepath.Join(snap, "A")); got != "v2" {
		t.Errorf("A = %q, want %q (committed value, not dirty working tree)", got, "v2")
	}
	// B (committed) must be present.
	if got := readFile(t, filepath.Join(snap, "B")); got != "b" {
		t.Errorf("B = %q, want %q", got, "b")
	}
	// C (untracked) must NOT be present.
	if _, err := os.Stat(filepath.Join(snap, "C")); !os.IsNotExist(err) {
		t.Errorf("untracked file C should not be in snapshot, stat err = %v", err)
	}

	cleanup()
	if _, err := os.Stat(snap); !os.IsNotExist(err) {
		t.Errorf("snapshot dir should be removed after cleanup, stat err = %v", err)
	}
}

func TestMaterializeCleanSnapshot_BadRef(t *testing.T) {
	repo, _ := initSnapshotTestRepo(t)

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	defer cleanup()
	if err == nil {
		t.Fatalf("expected error for nonexistent ref, got nil (snap=%q)", snap)
	}
	if snap != "" {
		t.Errorf("expected empty snapshot dir on error, got %q", snap)
	}
}

func TestMaterializeCleanSnapshot_NonGitDir(t *testing.T) {
	dir := t.TempDir() // not a git repo

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), dir, "HEAD")
	defer cleanup()
	if err == nil {
		t.Fatalf("expected error for non-git dir, got nil (snap=%q)", snap)
	}
	if snap != "" {
		t.Errorf("expected empty snapshot dir on error, got %q", snap)
	}
}
