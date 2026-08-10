package grpc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
)

// RepoSeed pins a repository's state for a clean snapshot: the commit to
// materialize and, when HEAD was on a branch at capture time, the branch name
// to recreate in the snapshot so in-container git operations see a familiar
// checkout instead of a detached HEAD.
type RepoSeed struct {
	SHA    string
	Branch string
}

// ChildRepoSeed identifies a nested [[repositories]] checkout to include in a
// clean project snapshot: its config name, its path relative to the project
// root, and its pinned state. Nested repos are invisible to the parent repo
// (their paths are gitignored), so each one is materialized separately.
type ChildRepoSeed struct {
	Name string
	Path string
	Seed RepoSeed
}

// materializeCleanSnapshot produces a temporary directory containing a real
// git checkout of projectDir pinned at seed.SHA, via a local `git clone`
// (object store hardlinked, so cheap even for large repos). Cloning rather
// than `git archive` keeps `.git` present — workflows run git commands inside
// the container (commit results, inspect history), which a bare archived tree
// broke. Working-tree mutations and untracked/gitignored files in the live
// tree are deliberately absent, so seeding a container from this snapshot is
// not corrupted by host workflow steps that mutate the live tree.
//
// children lists nested [[repositories]] checkouts (see childRepoSeeds); each
// is cloned into the snapshot at its declared path at its own pinned state. A
// child that fails to clone fails the whole snapshot: a partial snapshot
// missing a declared repo would break workflows that depend on it, whereas
// callers fall back to the live tree (which contains the checkout) on error.
//
// It returns the snapshot directory, a cleanup func (os.RemoveAll on the temp
// dir), and any error. On error it returns ("", a no-op cleanup, err) so
// callers can always defer cleanup() unconditionally.
func materializeCleanSnapshot(ctx context.Context, projectDir string, seed RepoSeed, children []ChildRepoSeed) (snapshotDir string, cleanup func(), err error) {
	noop := func() {}

	tmp, err := os.MkdirTemp("", "cloche-snapshot-")
	if err != nil {
		return "", noop, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	if err := cloneAt(ctx, projectDir, seed, tmp); err != nil {
		cleanup()
		return "", noop, err
	}

	for _, c := range children {
		dest, err := safeJoin(tmp, c.Path)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("child repo %q: %w", c.Path, err)
		}
		if err := cloneAt(ctx, filepath.Join(projectDir, c.Path), c.Seed, dest); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("child repo %q: %w", c.Path, err)
		}
	}

	return tmp, cleanup, nil
}

// cloneAt clones src into dst (a nonexistent or empty directory) and checks
// out seed.SHA — on a branch named seed.Branch when set, detached otherwise.
// The clone is local, so git hardlinks the object store instead of copying.
func cloneAt(ctx context.Context, src string, seed RepoSeed, dst string) error {
	if seed.SHA == "" {
		return fmt.Errorf("clone %s: empty seed SHA", src)
	}
	if out, err := exec.CommandContext(ctx, "git", "clone", "--quiet", "--no-checkout", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s: %s: %w", src, strings.TrimSpace(string(out)), err)
	}
	args := []string{"-C", dst, "checkout", "--quiet"}
	if seed.Branch != "" {
		args = append(args, "-B", seed.Branch, seed.SHA)
	} else {
		args = append(args, "--detach", seed.SHA)
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s in clone of %s: %s: %w", seed.SHA, src, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// captureRepoSeed records dir's current HEAD commit and branch (empty when
// detached). Returns a zero RepoSeed when dir has no resolvable HEAD.
func captureRepoSeed(dir string) RepoSeed {
	sha := gitHEAD(dir)
	if sha == "" {
		return RepoSeed{}
	}
	return RepoSeed{SHA: sha, Branch: gitBranch(dir)}
}

// gitBranch returns the branch dir's HEAD is on, or "" when detached or not a
// repo.
func gitBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// childRepoSeeds resolves the project's [[repositories]] entries into snapshot
// seeds, capturing each nested checkout's current state. Entries with no path,
// paths outside the project root (never part of the container copy), or paths
// that don't exist on disk (not cloned yet) are skipped. A declared checkout
// that exists but is not a git repo with a resolvable HEAD is an error: the
// caller must not use a clean snapshot that would silently omit it, and
// should fall back to the live tree instead.
func childRepoSeeds(projectDir string) ([]ChildRepoSeed, error) {
	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, nil // no project config → no declared child repos
	}
	var seeds []ChildRepoSeed
	for _, r := range cfg.Repositories {
		if r.Path == "" {
			continue
		}
		abs := filepath.Join(projectDir, r.Path)
		rel, err := filepath.Rel(projectDir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		seed := nestedRepoSeed(abs)
		if seed.SHA == "" {
			return nil, fmt.Errorf("repository %q at %s exists but is not a git checkout with a resolvable HEAD", r.Name, r.Path)
		}
		seeds = append(seeds, ChildRepoSeed{Name: r.Name, Path: rel, Seed: seed})
	}
	return seeds, nil
}

// filterChildSeeds returns the subset of children whose Name is in names.
// A nil names slice means "no restriction" and returns children unchanged.
// Names with no matching child are ignored: childRepoSeeds already skips
// declared repos that aren't cloned on disk yet, and unknown names are
// rejected at extraction time by resolveRepos.
func filterChildSeeds(children []ChildRepoSeed, names []string) []ChildRepoSeed {
	if names == nil {
		return children
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []ChildRepoSeed
	for _, c := range children {
		if want[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// nestedRepoSeed captures dir's seed state, but only if dir is itself the top
// level of a git repository. A plain directory nested inside a git repo
// resolves rev-parse against the PARENT repo (git walks up), which would
// silently seed the child with the wrong tree — so require an exact toplevel
// match.
func nestedRepoSeed(dir string) RepoSeed {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return RepoSeed{}
	}
	top, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	abs, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil || top != abs {
		return RepoSeed{}
	}
	return captureRepoSeed(dir)
}

// safeJoin joins name onto base, rejecting paths that escape base.
func safeJoin(base, name string) (string, error) {
	target := filepath.Join(base, name)
	cleanBase := filepath.Clean(base) + string(os.PathSeparator)
	if target != filepath.Clean(base) && !hasPrefix(target, cleanBase) {
		return "", fmt.Errorf("path %q escapes destination", name)
	}
	return target, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
