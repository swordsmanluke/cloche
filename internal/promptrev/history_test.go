package promptrev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockGit replaces PATH with a directory containing a fake "git" that
// records every invocation (as its argv, newline-separated) to a log file
// instead of running anything, so a test can assert that no subprocess was
// spawned during a section of code that's expected to hit a warm cache.
// Restores the original PATH on test cleanup.
func blockGit(t *testing.T) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 1\n", logPath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755))

	origPath := os.Getenv("PATH")
	require.NoError(t, os.Setenv("PATH", dir))
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	return func() []string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

func TestHistoryCache_CachesUntilHEADMoves(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("v1"), 0644))
	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "v1")
	rev1 := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	cache := NewHistoryCache()
	commits := cache.History(dir, "prompt.md")
	require.Len(t, commits, 1)
	assert.Equal(t, rev1, commits[0].SHA)

	// A second call at the same HEAD must not shell out at all: it's
	// satisfied entirely from the cache plus a filesystem HEAD read.
	calls := blockGit(t)
	commits = cache.History(dir, "prompt.md")
	require.Len(t, commits, 1)
	assert.Equal(t, rev1, commits[0].SHA)
	assert.Empty(t, calls(), "History invoked git while HEAD was unchanged")
}

func TestHistoryCache_InvalidatesWhenHEADMoves(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("v1"), 0644))
	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "v1")

	cache := NewHistoryCache()
	commits := cache.History(dir, "prompt.md")
	require.Len(t, commits, 1)

	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, os.WriteFile(promptPath, []byte("v2"), 0644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "v2")
	rev2 := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	commits = cache.History(dir, "prompt.md")
	require.Len(t, commits, 2)
	assert.Equal(t, rev2, commits[0].SHA)
}

func TestHeadSHA_MatchesGitRevParse(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0644))
	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	want := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	assert.Equal(t, want, headSHA(dir))
}
