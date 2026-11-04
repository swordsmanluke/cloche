package docker

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFiles(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0644))
	}
}

func tarFiles(t *testing.T, projDir string, patterns []ignorePattern) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, writeTarFromProject(tw, projDir, patterns))
	require.NoError(t, tw.Close())
	files, _ := collectTarEntries(t, &buf)
	return files
}

func TestCopyOnlyPatterns_KeepsOnlyTheNamedPaths(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir,
		".cloche/prompts/implement.md",
		".git/HEAD",
		".clocheignore",
		"CLAUDE.md",
		"balance/report.txt",
		"notes.md",
		"repos/pickup/.git/HEAD",
		"repos/pickup/main.go",
		"repos/voxline/huge.bin",
		"repos/README.md",
	)

	patterns, err := copyOnlyPatterns(dir, []string{".cloche", ".clocheignore", "CLAUDE.md", "AGENTS.md", "repos/pickup"})
	require.NoError(t, err)
	files := tarFiles(t, dir, patterns)

	for _, kept := range []string{".cloche/prompts/implement.md", ".clocheignore", "CLAUDE.md", "repos/pickup/main.go", "repos/pickup/.git/HEAD"} {
		assert.Contains(t, files, kept)
	}
	for _, left := range []string{".git/HEAD", "balance/report.txt", "notes.md", "repos/voxline/huge.bin", "repos/README.md"} {
		assert.NotContains(t, files, left)
	}
}

func TestCopyOnlyPatterns_ClocheignoreStillAppliesInsideKeptPaths(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, ".clocheignore", ".cloche/logs/run.log", ".cloche/scripts/a.sh", "other.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".clocheignore"), []byte(".cloche/logs/\n"), 0644))

	patterns, err := parseClocheignore(dir)
	require.NoError(t, err)
	only, err := copyOnlyPatterns(dir, []string{".cloche", ".clocheignore"})
	require.NoError(t, err)
	files := tarFiles(t, dir, append(patterns, only...))

	assert.Contains(t, files, ".cloche/scripts/a.sh")
	assert.NotContains(t, files, ".cloche/logs/run.log")
	assert.NotContains(t, files, "other.txt")
}

func TestCopyOnlyPatterns_MissingKeptPathIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, ".cloche/a.sh")

	patterns, err := copyOnlyPatterns(dir, []string{".cloche", "CLAUDE.md", "repos/absent"})
	require.NoError(t, err)

	assert.Contains(t, tarFiles(t, dir, patterns), ".cloche/a.sh")
}
