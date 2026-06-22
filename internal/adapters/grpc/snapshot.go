package grpc

import (
	"context"
	"os"
	"path/filepath"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/runcontext"
)

// shouldCaptureSnapshot reports whether a step result warrants a workspace
// snapshot: the run must be a non-host container run, and the step must have
// succeeded (not failed/errored and not skipped).
func shouldCaptureSnapshot(result *pb.StepResult, run *domain.Run) bool {
	if result == nil || run == nil {
		return false
	}
	if run.IsHost {
		return false
	}
	// Without a project directory there is nowhere stable to store the snapshot;
	// skip rather than writing a relative path into the daemon's cwd.
	if run.ProjectDir == "" {
		return false
	}
	if result.Skipped {
		return false
	}
	return result.Result != "fail" && result.Result != "error"
}

// snapshotDir returns the per-attempt directory that holds workspace snapshots
// for a resumable run: .cloche/runs/<taskID>/snapshots/<attemptID>/.
func snapshotDir(projectDir, taskID, attemptID string) string {
	return filepath.Join(runcontext.RunDir(projectDir, taskID), "snapshots", attemptID)
}

// snapshotPathForStep returns the tarball path for a single step's snapshot.
func snapshotPathForStep(projectDir, taskID, attemptID, stepName string) string {
	return filepath.Join(snapshotDir(projectDir, taskID, attemptID), stepName+".tar")
}

// latestSnapshotPath returns the path of the newest (by mtime) .tar snapshot for
// the given attempt, or "" if none exist (or the directory is absent).
func latestSnapshotPath(projectDir, taskID, attemptID string) string {
	dir := snapshotDir(projectDir, taskID, attemptID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".tar" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest = filepath.Join(dir, e.Name())
			newestMod = info.ModTime()
		}
	}
	return newest
}

// captureWorkspaceSnapshot streams the container's /workspace/ to a per-attempt,
// per-step tarball on the host. Best-effort: the caller should log but not fail
// on error.
func (s *ClocheServer) captureWorkspaceSnapshot(ctx context.Context, containerID, projectDir, taskID, attemptID, stepName string) error {
	dir := snapshotDir(projectDir, taskID, attemptID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := snapshotPathForStep(projectDir, taskID, attemptID, stepName)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.pool.CopyTarFrom(ctx, containerID, "/workspace", f); err != nil {
		return err
	}
	return f.Close()
}

// injectWorkspaceSnapshot streams a previously-captured workspace tarball into a
// freshly-built container's /workspace/.
func (s *ClocheServer) injectWorkspaceSnapshot(ctx context.Context, containerID, tarPath string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.pool.CopyTarTo(ctx, containerID, f, "/workspace/")
}
