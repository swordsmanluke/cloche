package grpc

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
)

// ChildRepoSeed identifies a nested [[repositories]] checkout to include in a
// clean project snapshot: its path relative to the project root and the commit
// to materialize it at. Nested repos are invisible to the parent's
// `git archive` (their paths are gitignored gitlinks at best), so each one is
// archived separately at its own HEAD.
type ChildRepoSeed struct {
	Path string
	SHA  string
}

// materializeCleanSnapshot produces a temporary directory containing the
// tracked contents of projectDir exactly as committed at ref, using
// `git archive`. This deliberately ignores working-tree mutations and
// untracked/gitignored files, so seeding a container from this snapshot is not
// corrupted by host workflow steps that mutate the live tree.
//
// children lists nested [[repositories]] checkouts (see childRepoSeeds); each
// is materialized into the snapshot at its declared path via its own
// `git archive`, since the parent archive cannot see inside a nested repo. A
// child that fails to archive fails the whole snapshot: a partial snapshot
// missing a declared repo would break workflows that depend on it, whereas
// callers fall back to the live tree (which contains the checkout) on error.
//
// It returns the snapshot directory, a cleanup func (os.RemoveAll on the temp
// dir), and any error. On error it returns ("", a no-op cleanup, err) so
// callers can always defer cleanup() unconditionally.
func materializeCleanSnapshot(ctx context.Context, projectDir, ref string, children []ChildRepoSeed) (snapshotDir string, cleanup func(), err error) {
	noop := func() {}

	tmp, err := os.MkdirTemp("", "cloche-snapshot-")
	if err != nil {
		return "", noop, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	if err := archiveRepoInto(ctx, projectDir, ref, tmp); err != nil {
		cleanup()
		return "", noop, err
	}

	for _, c := range children {
		dest, err := safeJoin(tmp, c.Path)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("child repo %q: %w", c.Path, err)
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("child repo %q: %w", c.Path, err)
		}
		if err := archiveRepoInto(ctx, filepath.Join(projectDir, c.Path), c.SHA, dest); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("child repo %q: %w", c.Path, err)
		}
	}

	return tmp, cleanup, nil
}

// nestedRepoHEAD returns dir's HEAD commit, but only if dir is itself the
// top level of a git repository. A plain directory nested inside a git repo
// resolves rev-parse against the PARENT repo (git walks up), which would
// silently seed the child with the wrong tree — so require an exact
// toplevel match.
func nestedRepoHEAD(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	top, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	abs, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil || top != abs {
		return ""
	}
	return gitHEAD(dir)
}

// archiveRepoInto extracts `git archive <ref>` of repoDir into dst.
func archiveRepoInto(ctx context.Context, repoDir, ref, dst string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "archive", "--format=tar", ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git archive stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}

	extractErr := extractTar(stdout, dst)
	// Drain/close handled by Wait; capture archive exit status too.
	waitErr := cmd.Wait()

	if extractErr != nil {
		return fmt.Errorf("extract git archive: %w", extractErr)
	}
	if waitErr != nil {
		return fmt.Errorf("git archive %s in %s: %w", ref, repoDir, waitErr)
	}
	return nil
}

// childRepoSeeds resolves the project's [[repositories]] entries into snapshot
// seeds, capturing each nested checkout's current HEAD. Entries with no path,
// paths outside the project root (never part of the container copy), or paths
// that don't exist on disk (not cloned yet) are skipped. A declared checkout
// that exists but has no resolvable HEAD is an error: the caller must not use
// a clean snapshot that would silently omit it, and should fall back to the
// live tree instead.
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
		sha := nestedRepoHEAD(abs)
		if sha == "" {
			return nil, fmt.Errorf("repository %q at %s exists but is not a git checkout with a resolvable HEAD", r.Name, r.Path)
		}
		seeds = append(seeds, ChildRepoSeed{Path: rel, SHA: sha})
	}
	return seeds, nil
}

// extractTar reads a tar stream and writes its entries beneath dst. Entry paths
// are constrained to dst to guard against path traversal.
func extractTar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// Best-effort: remove any existing entry first.
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// Skip other entry types (e.g. hardlinks, devices) — git archive of
			// a normal tree does not emit them.
		}
	}
}

// safeJoin joins name onto base, rejecting paths that escape base.
func safeJoin(base, name string) (string, error) {
	target := filepath.Join(base, name)
	cleanBase := filepath.Clean(base) + string(os.PathSeparator)
	if target != filepath.Clean(base) && !hasPrefix(target, cleanBase) {
		return "", fmt.Errorf("tar entry %q escapes destination", name)
	}
	return target, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
