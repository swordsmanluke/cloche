package scan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
)

func TestCollectMulti_RootOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644))

	c, next, err := scan.CollectMulti(scan.RepoInput{Dir: dir}, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, c.Docs, 1)
	assert.Equal(t, "CLAUDE.md", c.Docs[0].Path)

	require.Len(t, next.LastScanStats.Repos, 1)
	root := next.LastScanStats.Repos[0]
	assert.Equal(t, "", root.Name)
	assert.Equal(t, 1, root.DocsNew)
	assert.False(t, root.Empty())
}

func TestCollectMulti_NamespacesNonRootRepo(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repos", "docs-repo")
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "CLAUDE.md"), []byte("repo rules"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".cloche", "runs", "run-1"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".cloche", "runs", "run-1", "task_prompt.md"), []byte("do it"), 0644))

	repos := []scan.RepoInput{{Name: "docs-repo", Dir: repoDir}}
	c, next, err := scan.CollectMulti(scan.RepoInput{Dir: dir}, repos, nil, nil, nil)
	require.NoError(t, err)

	var docPaths, runIDs []string
	for _, d := range c.Docs {
		docPaths = append(docPaths, d.Path)
	}
	for _, r := range c.Runs {
		runIDs = append(runIDs, r.ID)
	}
	assert.Contains(t, docPaths, filepath.ToSlash(filepath.Join("repos", "docs-repo", "CLAUDE.md")))
	assert.Contains(t, runIDs, filepath.ToSlash(filepath.Join("docs-repo", "run-1")))

	require.Len(t, next.LastScanStats.Repos, 2)
	var repoStats intent.RepoStats
	for _, s := range next.LastScanStats.Repos {
		if s.Name == "docs-repo" {
			repoStats = s
		}
	}
	assert.Equal(t, 1, repoStats.DocsNew)
	assert.Equal(t, 1, repoStats.Runs)
	assert.False(t, repoStats.Empty())

	// The sub-repo's cursor advanced independently under next.Repos, using
	// its own repo-relative doc path (not the namespaced Write() path).
	require.NotNil(t, next.Repos["docs-repo"])
	assert.Contains(t, next.Repos["docs-repo"].ScannedDocs, "CLAUDE.md")
	assert.Contains(t, next.Repos["docs-repo"].ScannedRuns, "run-1")

	// Re-collecting from the persisted state is quiet for both root and repo.
	c2, _, err := scan.CollectMulti(scan.RepoInput{Dir: dir}, repos, next, nil, nil)
	require.NoError(t, err)
	assert.False(t, c2.HasNew())
}

func TestCollectMulti_EmptyRepoFlagged(t *testing.T) {
	dir := t.TempDir()
	emptyRepoDir := filepath.Join(dir, "repos", "empty-repo")
	require.NoError(t, os.MkdirAll(emptyRepoDir, 0755))

	repos := []scan.RepoInput{{Name: "empty-repo", Dir: emptyRepoDir}}
	_, next, err := scan.CollectMulti(scan.RepoInput{Dir: dir}, repos, nil, nil, nil)
	require.NoError(t, err)

	require.Len(t, next.LastScanStats.Repos, 2)
	assert.Equal(t, []string{"empty-repo"}, next.LastScanStats.EmptyRepoNames())
}

func TestCollectMulti_CommitRange(t *testing.T) {
	dir := initGitProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0644))
	runGitCmd(t, dir, "add", "a.txt")
	runGitCmd(t, dir, "commit", "-q", "-m", "first")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2"), 0644))
	runGitCmd(t, dir, "add", "a.txt")
	runGitCmd(t, dir, "commit", "-q", "-m", "second")

	_, next, err := scan.CollectMulti(scan.RepoInput{Dir: dir}, nil, nil, nil, nil)
	require.NoError(t, err)

	require.Len(t, next.LastScanStats.Repos, 1)
	root := next.LastScanStats.Repos[0]
	assert.Equal(t, 2, root.Commits)
	assert.Contains(t, root.CommitRange, "..")
}
