package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProjectStore implements projectLister with a fixed project list.
type mockProjectStore struct {
	projects []string
}

func (m *mockProjectStore) ListProjects(ctx context.Context) ([]string, error) {
	return m.projects, nil
}

// enableLoopRecorder records which project dirs were passed to enableLoop.
type enableLoopRecorder struct {
	mu      sync.Mutex
	enabled []string
}

func (r *enableLoopRecorder) enable(ctx context.Context, projectDir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = append(r.enabled, projectDir)
	return nil
}

// makeProject creates a temp directory that looks like an active cloche project:
// .cloche/config.toml and .cloche/host.cloche.
func makeProject(t *testing.T, active bool) string {
	t.Helper()
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0o755))

	activeStr := "false"
	if active {
		activeStr = "true"
	}
	err := os.WriteFile(filepath.Join(clocheDir, "config.toml"),
		[]byte("active = "+activeStr+"\n"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(`
workflow "main" {
  step done { }
}
`), 0o644)
	require.NoError(t, err)

	return dir
}

// TestAutoRunActiveProjects_NoFilter_StartsOnlyActiveProjects verifies that
// without a project filter, only projects with active=true are enabled.
func TestAutoRunActiveProjects_NoFilter_StartsOnlyActiveProjects(t *testing.T) {
	activeDir := makeProject(t, true)
	inactiveDir := makeProject(t, false)

	rec := &enableLoopRecorder{}
	mock := &mockProjectStore{projects: []string{activeDir, inactiveDir}}

	autoRunActiveProjects(mock, rec.enable, "")

	assert.Equal(t, []string{activeDir}, rec.enabled,
		"only the active project should have been enabled")
}

// TestAutoRunActiveProjects_NoFilter_SkipsProjectWithoutHostCloche verifies
// that a project missing host.cloche is skipped even when active=true.
func TestAutoRunActiveProjects_NoFilter_SkipsProjectWithoutHostCloche(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "config.toml"),
		[]byte("active = true\n"), 0o644))
	// No host.cloche written.

	rec := &enableLoopRecorder{}
	mock := &mockProjectStore{projects: []string{dir}}

	autoRunActiveProjects(mock, rec.enable, "")

	assert.Empty(t, rec.enabled, "project without host.cloche should be skipped")
}

// TestAutoRunActiveProjects_WithFilter_ScopesToSingleProject verifies that
// --project scopes startup to exactly the given project, ignoring the store.
func TestAutoRunActiveProjects_WithFilter_ScopesToSingleProject(t *testing.T) {
	target := makeProject(t, true)
	other := makeProject(t, true)

	// Store knows about both, but the filter should restrict to target only.
	rec := &enableLoopRecorder{}
	mock := &mockProjectStore{projects: []string{target, other}}

	autoRunActiveProjects(mock, rec.enable, target)

	require.Len(t, rec.enabled, 1)
	assert.Equal(t, target, rec.enabled[0])
}

// TestAutoRunActiveProjects_WithFilter_BypassesActiveCheck verifies that
// --project enables the loop even when the project has active=false.
func TestAutoRunActiveProjects_WithFilter_BypassesActiveCheck(t *testing.T) {
	inactiveDir := makeProject(t, false)

	rec := &enableLoopRecorder{}
	mock := &mockProjectStore{projects: []string{inactiveDir}}

	autoRunActiveProjects(mock, rec.enable, inactiveDir)

	require.Len(t, rec.enabled, 1, "filter should bypass active=false check")
	assert.Equal(t, inactiveDir, rec.enabled[0])
}

// TestAutoRunActiveProjects_WithFilter_IgnoresOtherActiveProjects ensures that
// when --project is given, other active projects in the store are not started.
func TestAutoRunActiveProjects_WithFilter_IgnoresOtherActiveProjects(t *testing.T) {
	target := makeProject(t, true)
	sibling := makeProject(t, true)

	rec := &enableLoopRecorder{}
	mock := &mockProjectStore{projects: []string{target, sibling}}

	autoRunActiveProjects(mock, rec.enable, target)

	assert.Len(t, rec.enabled, 1)
	assert.Equal(t, target, rec.enabled[0], "only the target project should be enabled")
}
