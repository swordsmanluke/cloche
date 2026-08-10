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

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: head}, nil)
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

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}, nil)
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

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), dir, RepoSeed{SHA: "HEAD"}, nil)
	defer cleanup()
	if err == nil {
		t.Fatalf("expected error for non-git dir, got nil (snap=%q)", snap)
	}
	if snap != "" {
		t.Errorf("expected empty snapshot dir on error, got %q", snap)
	}
}

// initNestedRepo creates a gitignored nested git repo at parent/<rel> with one
// committed file F=committed and a dirty working-tree change (F=dirty on disk).
// Returns the nested repo's HEAD.
func initNestedRepo(t *testing.T, parent, rel string) string {
	t.Helper()
	dir := filepath.Join(parent, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "F"), []byte("committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "F")
	run("git", "commit", "-m", "F")
	head := run("git", "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "F"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	return head
}

func TestMaterializeCleanSnapshot_IncludesChildRepos(t *testing.T) {
	repo, head := initSnapshotTestRepo(t)
	childHead := initNestedRepo(t, repo, filepath.Join("repos", "child"))

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: head},
		[]ChildRepoSeed{{Path: filepath.Join("repos", "child"), Seed: RepoSeed{SHA: childHead}}})
	if err != nil {
		t.Fatalf("materializeCleanSnapshot: %v", err)
	}
	defer cleanup()

	// Child repo content must be present, at its committed state (not dirty).
	if got := readFile(t, filepath.Join(snap, "repos", "child", "F")); got != "committed" {
		t.Errorf("child F = %q, want %q", got, "committed")
	}
	// Parent tracked file still present.
	if got := readFile(t, filepath.Join(snap, "A")); got != "v2" {
		t.Errorf("A = %q, want %q", got, "v2")
	}
}

func TestMaterializeCleanSnapshot_ChildArchiveFailureFailsSnapshot(t *testing.T) {
	repo, head := initSnapshotTestRepo(t)

	// Declared child that doesn't exist as a git repo.
	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: head},
		[]ChildRepoSeed{{Path: filepath.Join("repos", "ghost"), Seed: RepoSeed{SHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}}})
	defer cleanup()
	if err == nil {
		t.Fatalf("expected error for unarchivable child repo, got nil (snap=%q)", snap)
	}
}

func TestMaterializeCleanSnapshot_ChildPathEscapeRejected(t *testing.T) {
	repo, head := initSnapshotTestRepo(t)

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: head},
		[]ChildRepoSeed{{Path: filepath.Join("..", "outside"), Seed: RepoSeed{SHA: head}}})
	defer cleanup()
	if err == nil {
		t.Fatalf("expected error for escaping child path, got nil (snap=%q)", snap)
	}
}

func TestChildRepoSeeds_ResolvesDeclaredRepos(t *testing.T) {
	repo, _ := initSnapshotTestRepo(t)
	childHead := initNestedRepo(t, repo, filepath.Join("repos", "child"))

	if err := os.MkdirAll(filepath.Join(repo, ".cloche"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgToml := "[[repositories]]\nname = \"child\"\npath = \"./repos/child\"\n" +
		"[[repositories]]\nname = \"uncloned\"\npath = \"./repos/uncloned\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".cloche", "config.toml"), []byte(cfgToml), 0o644); err != nil {
		t.Fatal(err)
	}

	seeds, err := childRepoSeeds(repo)
	if err != nil {
		t.Fatalf("childRepoSeeds: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("seeds = %+v, want exactly the cloned child", seeds)
	}
	if seeds[0].Path != filepath.Join("repos", "child") || seeds[0].Seed.SHA != childHead {
		t.Errorf("seed = %+v, want {repos/child %s}", seeds[0], childHead)
	}
}

func TestChildRepoSeeds_NonRepoCheckoutIsError(t *testing.T) {
	repo, _ := initSnapshotTestRepo(t)

	// Declared path exists on disk but is not a git repo.
	if err := os.MkdirAll(filepath.Join(repo, "repos", "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".cloche"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgToml := "[[repositories]]\nname = \"plain\"\npath = \"./repos/plain\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".cloche", "config.toml"), []byte(cfgToml), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := childRepoSeeds(repo); err == nil {
		t.Fatal("expected error for existing non-repo checkout, got nil")
	}
}

func TestMaterializeCleanSnapshot_ProducesGitCheckout(t *testing.T) {
	repo, _ := initSnapshotTestRepo(t)
	childHead := initNestedRepo(t, repo, filepath.Join("repos", "child"))

	// Mirror real projects: the nested checkout lives at a gitignored path.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("repos/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn("add", ".gitignore")
	gitIn("commit", "-m", "ignore repos/")
	head := gitIn("rev-parse", "HEAD")

	snap, cleanup, err := materializeCleanSnapshot(context.Background(), repo, RepoSeed{SHA: head, Branch: "main"},
		[]ChildRepoSeed{{Path: filepath.Join("repos", "child"), Seed: RepoSeed{SHA: childHead}}})
	if err != nil {
		t.Fatalf("materializeCleanSnapshot: %v", err)
	}
	defer cleanup()

	// The snapshot must be a functioning git repo pinned at head: workflows
	// run git commands (add/commit) inside the container copy.
	gitOut := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %s: %v", args, dir, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := gitOut(snap, "rev-parse", "HEAD"); got != head {
		t.Errorf("snapshot HEAD = %s, want %s", got, head)
	}
	if got := gitOut(snap, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("snapshot branch = %q, want main", got)
	}
	if got := gitOut(snap, "status", "--porcelain"); got != "" {
		t.Errorf("snapshot working tree not clean: %q", got)
	}
	// Nested repo is its own functioning checkout at its pinned commit.
	child := filepath.Join(snap, "repos", "child")
	if got := gitOut(child, "rev-parse", "HEAD"); got != childHead {
		t.Errorf("child HEAD = %s, want %s", got, childHead)
	}
	// And in-container-style commits must work in both.
	if err := os.WriteFile(filepath.Join(child, "G"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(child, "add", "G")
	gitOut(child, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "in-snapshot commit")
}

func TestFilterChildSeeds(t *testing.T) {
	children := []ChildRepoSeed{
		{Name: "a", Path: "repos/a"},
		{Name: "b", Path: "repos/b"},
	}

	// nil names → no restriction, everything included.
	if got := filterChildSeeds(children, nil); len(got) != 2 {
		t.Fatalf("nil names: got %+v, want all children", got)
	}
	// Subset selection by name.
	got := filterChildSeeds(children, []string{"b"})
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("[b]: got %+v, want just b", got)
	}
	// Names without a matching child (declared but not cloned) are ignored.
	got = filterChildSeeds(children, []string{"a", "uncloned"})
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("[a uncloned]: got %+v, want just a", got)
	}
	// Empty (non-nil) names → no children at all.
	if got := filterChildSeeds(children, []string{}); len(got) != 0 {
		t.Fatalf("empty names: got %+v, want none", got)
	}
}
