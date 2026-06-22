package grpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/runcontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// tarCopyRuntime is a recordingContainerRuntime that also supports tar-stream
// copy, used to exercise captureWorkspaceSnapshot / injectWorkspaceSnapshot.
type tarCopyRuntime struct {
	recordingContainerRuntime

	tarFromPayload string // bytes written to w on CopyTarFrom

	tarFromID  string
	tarFromSrc string

	tarToID       string
	tarToDst      string
	tarToReceived string
}

func (r *tarCopyRuntime) CopyTarFrom(_ context.Context, containerID, srcPath string, w io.Writer) error {
	r.tarFromID = containerID
	r.tarFromSrc = srcPath
	_, err := io.WriteString(w, r.tarFromPayload)
	return err
}

func (r *tarCopyRuntime) CopyTarTo(_ context.Context, containerID string, rd io.Reader, dst string) error {
	r.tarToID = containerID
	r.tarToDst = dst
	b, err := io.ReadAll(rd)
	r.tarToReceived = string(b)
	return err
}

// commitRuntime supports CommitContainer/RemoveImage (containerResumer) and tar
// copy, and records how many times CommitContainer is called. Start succeeds and
// auto-notifies the pool that the container is ready so SessionFor does not block.
type commitRuntime struct {
	tarCopyRuntime
	pool         *docker.ContainerPool
	commitCalled int
	idCounter    int
	failAfter    int // after this many Starts, return an error
}

func (r *commitRuntime) Start(_ context.Context, cfg ports.ContainerConfig) (string, error) {
	r.idCounter++
	// The first Start registers the old-attempt session for the test. Any later
	// Start (the resumed workflow goroutine) fails fast so the goroutine exits
	// cleanly instead of running the full engine and leaking.
	if r.failAfter > 0 && r.idCounter > r.failAfter {
		return "", fmt.Errorf("start disabled after %d containers", r.failAfter)
	}
	id := "cid-" + itoa(r.idCounter)
	r.bwInit(id)
	if r.pool != nil {
		go func() {
			time.Sleep(5 * time.Millisecond)
			r.pool.NotifyReady(id)
		}()
	}
	return id, nil
}

func (r *commitRuntime) CommitContainer(_ context.Context, containerID, attemptID string) (string, error) {
	r.commitCalled++
	return "cloche-resume:" + attemptID + "-" + containerID, nil
}

func (r *commitRuntime) RemoveImage(_ context.Context, _ string) error { return nil }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newSnapshotTestServer(rt ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{
		store:           &fakeRunStore{},
		container:       rt,
		pool:            docker.NewContainerPool(rt),
		runIDs:          make(map[string]string),
		containerRun:    make(map[string]string),
		hostCancels:     make(map[string]context.CancelFunc),
		loops:           make(map[string]*host.Loop),
		activityLoggers: make(map[string]*activitylog.Logger),
	}
}

func TestResumeRebuildModeFromContext(t *testing.T) {
	cases := []struct {
		name string
		md   metadata.MD
		want resumeRebuildMode
	}{
		{"absent", nil, resumeRebuild},
		{"empty-value", metadata.Pairs("x-cloche-resume-rebuild", ""), resumeRebuild},
		{"rebuild", metadata.Pairs("x-cloche-resume-rebuild", "rebuild"), resumeRebuild},
		{"reuse", metadata.Pairs("x-cloche-resume-rebuild", "reuse"), resumeReuse},
		{"clean", metadata.Pairs("x-cloche-resume-rebuild", "clean"), resumeClean},
		{"unknown", metadata.Pairs("x-cloche-resume-rebuild", "bogus"), resumeRebuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.md)
			}
			assert.Equal(t, tc.want, resumeRebuildModeFromContext(ctx))
		})
	}
}

func TestShouldCaptureSnapshot(t *testing.T) {
	containerRun := &domain.Run{IsHost: false, ProjectDir: "/proj"}
	hostRun := &domain.Run{IsHost: true, ProjectDir: "/proj"}

	cases := []struct {
		name   string
		result *pb.StepResult
		run    *domain.Run
		want   bool
	}{
		{"success-container", &pb.StepResult{Result: "success"}, containerRun, true},
		{"custom-result-container", &pb.StepResult{Result: "approved"}, containerRun, true},
		{"fail-container", &pb.StepResult{Result: "fail"}, containerRun, false},
		{"error-container", &pb.StepResult{Result: "error"}, containerRun, false},
		{"skipped-container", &pb.StepResult{Result: "success", Skipped: true}, containerRun, false},
		{"success-host", &pb.StepResult{Result: "success"}, hostRun, false},
		{"no-project-dir", &pb.StepResult{Result: "success"}, &domain.Run{IsHost: false}, false},
		{"nil-result", nil, containerRun, false},
		{"nil-run", &pb.StepResult{Result: "success"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldCaptureSnapshot(tc.result, tc.run))
		})
	}
}

func TestModeUsesCommitAndSnapshot(t *testing.T) {
	assert.False(t, modeUsesCommit(resumeRebuild))
	assert.True(t, modeUsesCommit(resumeReuse))
	assert.False(t, modeUsesCommit(resumeClean))

	assert.True(t, modeUsesSnapshot(resumeRebuild))
	assert.True(t, modeUsesSnapshot(resumeReuse))
	assert.False(t, modeUsesSnapshot(resumeClean))
}

func TestLatestSnapshotPath(t *testing.T) {
	dir := t.TempDir()
	projectDir := dir
	taskID := "task-1"
	attemptID := "att-1"

	// No directory yet → "".
	assert.Equal(t, "", latestSnapshotPath(projectDir, taskID, attemptID))

	snapDir := snapshotDir(projectDir, taskID, attemptID)
	require.NoError(t, os.MkdirAll(snapDir, 0o755))

	older := filepath.Join(snapDir, "step-a.tar")
	newer := filepath.Join(snapDir, "step-b.tar")
	require.NoError(t, os.WriteFile(older, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(newer, []byte("b"), 0o644))

	// Force distinct mtimes: older in the past, newer now.
	past := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(older, past, past))

	// A non-.tar file should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "notes.txt"), []byte("x"), 0o644))

	assert.Equal(t, newer, latestSnapshotPath(projectDir, taskID, attemptID))
}

func TestCaptureWorkspaceSnapshot_WritesTar(t *testing.T) {
	rt := &tarCopyRuntime{tarFromPayload: "TAR-CONTENT"}
	srv := newSnapshotTestServer(rt)

	dir := t.TempDir()
	err := srv.captureWorkspaceSnapshot(context.Background(), "cid-1", dir, "task-1", "att-1", "build")
	require.NoError(t, err)

	// The runtime was asked for /workspace from the right container.
	assert.Equal(t, "cid-1", rt.tarFromID)
	assert.Equal(t, "/workspace", rt.tarFromSrc)

	// The tar was written to the expected per-step path.
	want := snapshotPathForStep(dir, "task-1", "att-1", "build")
	assert.Equal(t, filepath.Join(runcontext.RunDir(dir, "task-1"), "snapshots", "att-1", "build.tar"), want)
	data, err := os.ReadFile(want)
	require.NoError(t, err)
	assert.Equal(t, "TAR-CONTENT", string(data))
}

func TestInjectWorkspaceSnapshot_CopiesToWorkspace(t *testing.T) {
	rt := &tarCopyRuntime{}
	srv := newSnapshotTestServer(rt)

	dir := t.TempDir()
	tarPath := filepath.Join(dir, "snap.tar")
	require.NoError(t, os.WriteFile(tarPath, []byte("SNAP-BYTES"), 0o644))

	err := srv.injectWorkspaceSnapshot(context.Background(), "cid-2", tarPath)
	require.NoError(t, err)

	assert.Equal(t, "cid-2", rt.tarToID)
	assert.Equal(t, "/workspace/", rt.tarToDst)
	assert.Equal(t, "SNAP-BYTES", rt.tarToReceived)
}

func TestInjectWorkspaceSnapshot_MissingFile(t *testing.T) {
	rt := &tarCopyRuntime{}
	srv := newSnapshotTestServer(rt)
	err := srv.injectWorkspaceSnapshot(context.Background(), "cid", filepath.Join(t.TempDir(), "nope.tar"))
	require.Error(t, err)
}

// writeResumeProject writes a minimal container workflow so resumeContainerRunWithPool
// can load it via host.FindAllWorkflows.
func writeResumeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0o755))
	wf := `workflow develop {
  step build {
    run     = "echo building"
    results = [success, fail]
  }
  build:success -> done
  build:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(wf), 0o644))
	return dir
}

func newResumeForkServer(rt ports.ContainerRuntime, pool *docker.ContainerPool) *ClocheServer {
	return &ClocheServer{
		store:           &fakeRunStore{},
		container:       rt,
		pool:            pool,
		defaultImage:    "test-image:latest",
		runIDs:          make(map[string]string),
		containerRun:    make(map[string]string),
		hostCancels:     make(map[string]context.CancelFunc),
		loops:           make(map[string]*host.Loop),
		activityLoggers: make(map[string]*activitylog.Logger),
	}
}

func runResumeForkTest(t *testing.T, mode resumeRebuildMode) int {
	t.Helper()
	dir := writeResumeProject(t)

	rt := &commitRuntime{failAfter: 1}
	pool := docker.NewContainerPool(rt)
	rt.pool = pool

	// Register a session for the OLD attempt so CommitForResume has something to
	// commit when invoked.
	oldAttempt := "old-att"
	_, err := pool.SessionFor(context.Background(), oldAttempt, ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	srv := newResumeForkServer(rt, pool)

	oldRun := domain.NewRun("develop-old", "develop")
	oldRun.ProjectDir = dir
	oldRun.AttemptID = oldAttempt
	oldRun.State = domain.RunStateFailed

	_, err = srv.resumeContainerRunWithPool(context.Background(), oldRun, "build", mode)
	require.NoError(t, err)

	// CommitForResume runs synchronously before the background goroutine starts,
	// so the count is final immediately after the call returns.
	return rt.commitCalled
}

func TestResumeContainerRunWithPool_RebuildSkipsCommit(t *testing.T) {
	assert.Equal(t, 0, runResumeForkTest(t, resumeRebuild),
		"rebuild mode must NOT commit the failed attempt's containers")
}

func TestResumeContainerRunWithPool_CleanSkipsCommit(t *testing.T) {
	assert.Equal(t, 0, runResumeForkTest(t, resumeClean),
		"clean mode must NOT commit the failed attempt's containers")
}

func TestResumeContainerRunWithPool_ReuseCommits(t *testing.T) {
	assert.Equal(t, 1, runResumeForkTest(t, resumeReuse),
		"reuse mode must commit the failed attempt's container")
}
