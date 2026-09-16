package intent_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/intent"
)

func TestStore_MissingIntentDir_LoadsEmpty(t *testing.T) {
	dir := t.TempDir() // no .cloche/intent/ at all
	store := intent.NewStore(dir)

	reqs, err := store.ListRequirements()
	require.NoError(t, err)
	assert.Empty(t, reqs)

	dm, err := store.LoadDomains()
	require.NoError(t, err)
	assert.Equal(t, 1, dm.Version)
	assert.Empty(t, dm.Domains)

	st, err := store.LoadScanState()
	require.NoError(t, err)
	assert.Empty(t, st.LastCommit)
	assert.Empty(t, st.ScannedRuns)
	assert.Empty(t, st.ScannedDocs)
}

func TestStore_CreateAndListRequirements(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	req := &intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Never bump the major version unless explicitly told to.",
	}

	created, err := store.CreateRequirement(req)
	require.NoError(t, err)
	assert.Regexp(t, `^req-[0-9a-f]{4}$`, created.ID)
	assert.False(t, created.Created.IsZero())
	assert.False(t, created.Updated.IsZero())

	// File exists on disk at the expected path.
	path := filepath.Join(dir, ".cloche", "intent", "requirements", created.ID+".md")
	_, err = os.Stat(path)
	require.NoError(t, err)

	list, err := store.ListRequirements()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, created.ID, list[0].ID)
	assert.Equal(t, created.Body, list[0].Body)
}

func TestStore_CreateRequirement_RejectsPresetID(t *testing.T) {
	store := intent.NewStore(t.TempDir())
	_, err := store.CreateRequirement(&intent.Requirement{ID: "req-1234"})
	require.Error(t, err)
}

func TestStore_GetRequirement_NotFound(t *testing.T) {
	store := intent.NewStore(t.TempDir())
	_, err := store.GetRequirement("req-0000")
	require.Error(t, err)
}

func TestStore_GetRequirement_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceLow,
		Body:       "Some statement.",
	})
	require.NoError(t, err)

	got, err := store.GetRequirement(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created, got)
}

func TestStore_ListRequirements_MalformedFileYieldsError(t *testing.T) {
	dir := t.TempDir()
	reqDir := filepath.Join(dir, ".cloche", "intent", "requirements")
	require.NoError(t, os.MkdirAll(reqDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(reqDir, "req-bad0.md"), []byte("not frontmatter at all"), 0o644))

	store := intent.NewStore(dir)
	reqs, err := store.ListRequirements()
	require.Error(t, err)
	assert.Nil(t, reqs)
	assert.Contains(t, err.Error(), "req-bad0.md")
}

func TestStore_ListRequirements_UsesMtimeCache(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Original body.",
	})
	require.NoError(t, err)

	list, err := store.ListRequirements()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Original body.", list[0].Body)

	// Edit the file on disk directly (out from under the store) without
	// changing its mtime; the cached value should still be served.
	path := filepath.Join(dir, ".cloche", "intent", "requirements", created.ID+".md")
	info, err := os.Stat(path)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := []byte(string(data) + " Extra unread text.")
	require.NoError(t, os.WriteFile(path, edited, 0o644))
	require.NoError(t, os.Chtimes(path, info.ModTime(), info.ModTime()))

	list, err = store.ListRequirements()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Original body.", list[0].Body, "unchanged mtime should serve the cached parse")
}

func TestStore_SaveAndLoadDomains(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	dm := &intent.DomainMap{
		Version: 1,
		Domains: []intent.Domain{
			{Name: "versioning", Description: "Version string management.", Paths: []string{"internal/version/**"}},
			{Name: "workflow-dsl", Description: "The .cloche workflow DSL.", Paths: []string{"internal/dsl/**"}, UserEdited: true},
		},
	}

	require.NoError(t, store.SaveDomains(dm))

	got, err := store.LoadDomains()
	require.NoError(t, err)
	assert.Equal(t, dm, got)
}

func TestStore_SaveAndLoadScanState(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	st := &intent.ScanState{
		LastCommit:  "abc123",
		ScannedRuns: []string{"run-1", "run-2"},
		ScannedDocs: map[string]string{"CLAUDE.md": "deadbeef"},
		LastScanAt:  time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
	}

	require.NoError(t, store.SaveScanState(st))

	got, err := store.LoadScanState()
	require.NoError(t, err)
	assert.True(t, st.LastScanAt.Equal(got.LastScanAt))
	got.LastScanAt = st.LastScanAt // avoid time.Time equality gotchas (wall/monotonic)
	assert.Equal(t, st, got)
}

func TestStore_SaveAndLoadScanState_WithReposAndStats(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	st := &intent.ScanState{
		LastCommit:  "abc123",
		ScannedRuns: []string{"run-1"},
		ScannedDocs: map[string]string{"CLAUDE.md": "deadbeef"},
		Repos: map[string]*intent.RepoCursor{
			"docs-repo": {
				LastCommit:  "def456",
				ScannedRuns: []string{"run-2"},
				ScannedDocs: map[string]string{"README.md": "cafebabe"},
			},
		},
		LastScanStats: intent.ScanStats{Repos: []intent.RepoStats{
			{Name: "", DocsNew: 1, Commits: 2, Runs: 1, Bytes: 512},
			{Name: "docs-repo", DocsNew: 0, DocsChanged: 1, Commits: 0, Runs: 1, Bytes: 128},
		}},
	}

	require.NoError(t, store.SaveScanState(st))

	got, err := store.LoadScanState()
	require.NoError(t, err)
	got.LastScanAt = st.LastScanAt // avoid time.Time equality gotchas (wall/monotonic)
	assert.Equal(t, st, got)
}

func TestStore_Exists(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	assert.False(t, store.Exists())

	require.NoError(t, store.SaveDomains(&intent.DomainMap{Version: 1}))
	assert.True(t, store.Exists())
}
