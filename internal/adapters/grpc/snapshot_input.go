package grpc

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// materializeCleanSnapshot produces a temporary directory containing the
// tracked contents of projectDir exactly as committed at ref, using
// `git archive`. This deliberately ignores working-tree mutations and
// untracked/gitignored files, so seeding a container from this snapshot is not
// corrupted by host workflow steps that mutate the live tree.
//
// It returns the snapshot directory, a cleanup func (os.RemoveAll on the temp
// dir), and any error. On error it returns ("", a no-op cleanup, err) so
// callers can always defer cleanup() unconditionally.
func materializeCleanSnapshot(ctx context.Context, projectDir, ref string) (snapshotDir string, cleanup func(), err error) {
	noop := func() {}

	tmp, err := os.MkdirTemp("", "cloche-snapshot-")
	if err != nil {
		return "", noop, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	cmd := exec.CommandContext(ctx, "git", "-C", projectDir, "archive", "--format=tar", ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("git archive stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("start git archive: %w", err)
	}

	extractErr := extractTar(stdout, tmp)
	// Drain/close handled by Wait; capture archive exit status too.
	waitErr := cmd.Wait()

	if extractErr != nil {
		cleanup()
		return "", noop, fmt.Errorf("extract git archive: %w", extractErr)
	}
	if waitErr != nil {
		cleanup()
		return "", noop, fmt.Errorf("git archive %s in %s: %w", ref, projectDir, waitErr)
	}

	return tmp, cleanup, nil
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
