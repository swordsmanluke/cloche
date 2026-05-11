package sqlite_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryStore_RoundTrip(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	const projectDir = "/tmp/test-project"

	// Save two repositories.
	backend := domain.Repository{Name: "backend", Path: "./repos/backend", RemoteURL: "https://github.com/org/backend", IsDefault: true}
	frontend := domain.Repository{Name: "frontend", Path: "./repos/frontend", IsDefault: false}

	require.NoError(t, store.SaveRepository(projectDir, backend))
	require.NoError(t, store.SaveRepository(projectDir, frontend))

	// List should return both in alphabetical order.
	repos, err := store.ListRepositories(projectDir)
	require.NoError(t, err)
	require.Len(t, repos, 2)
	assert.Equal(t, "backend", repos[0].Name)
	assert.Equal(t, "./repos/backend", repos[0].Path)
	assert.Equal(t, "https://github.com/org/backend", repos[0].RemoteURL)
	assert.True(t, repos[0].IsDefault)
	assert.Equal(t, "frontend", repos[1].Name)
	assert.False(t, repos[1].IsDefault)

	// GetRepository returns the right record.
	got, err := store.GetRepository(projectDir, "backend")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, backend.Name, got.Name)
	assert.Equal(t, backend.Path, got.Path)
	assert.Equal(t, backend.RemoteURL, got.RemoteURL)
	assert.True(t, got.IsDefault)

	// GetRepository returns nil for unknown name.
	missing, err := store.GetRepository(projectDir, "doesnotexist")
	require.NoError(t, err)
	assert.Nil(t, missing)

	// DeleteRepository removes the record.
	require.NoError(t, store.DeleteRepository(projectDir, "backend"))
	repos, err = store.ListRepositories(projectDir)
	require.NoError(t, err)
	require.Len(t, repos, 1)
	assert.Equal(t, "frontend", repos[0].Name)
}

func TestRepositoryStore_AutoSeed(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	const projectDir = "/tmp/seed-project"

	// First access with no rows triggers auto-seeding.
	repos, err := store.ListRepositories(projectDir)
	require.NoError(t, err)
	require.Len(t, repos, 1, "expected 1 auto-seeded repository")

	seeded := repos[0]
	assert.True(t, seeded.IsDefault, "seeded repository must be default")
	assert.Equal(t, projectDir, seeded.Path, "seeded repository path must equal project dir")

	// Second call should return the same seeded record (no double-seeding).
	repos2, err := store.ListRepositories(projectDir)
	require.NoError(t, err)
	require.Len(t, repos2, 1)
}

func TestRepositoryStore_Upsert(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	const projectDir = "/tmp/upsert-project"

	repo := domain.Repository{Name: "main", Path: ".", IsDefault: true}
	require.NoError(t, store.SaveRepository(projectDir, repo))

	// Update the same record.
	repo.Path = "./subdir"
	repo.RemoteURL = "https://example.com/repo"
	require.NoError(t, store.SaveRepository(projectDir, repo))

	got, err := store.GetRepository(projectDir, "main")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "./subdir", got.Path)
	assert.Equal(t, "https://example.com/repo", got.RemoteURL)
}

func TestRepositoryStore_IsolatedByProject(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	const projA = "/tmp/proj-a"
	const projB = "/tmp/proj-b"

	repoA := domain.Repository{Name: "shared-name", Path: "./a", IsDefault: true}
	repoB := domain.Repository{Name: "shared-name", Path: "./b", IsDefault: false}

	require.NoError(t, store.SaveRepository(projA, repoA))
	require.NoError(t, store.SaveRepository(projB, repoB))

	gotA, err := store.GetRepository(projA, "shared-name")
	require.NoError(t, err)
	assert.Equal(t, "./a", gotA.Path)

	gotB, err := store.GetRepository(projB, "shared-name")
	require.NoError(t, err)
	assert.Equal(t, "./b", gotB.Path)
}
