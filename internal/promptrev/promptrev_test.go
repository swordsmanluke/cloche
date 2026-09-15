package promptrev

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func TestResolveFile(t *testing.T) {
	path, ok := ResolveFile(`file(".cloche/prompts/implement.md")`)
	require.True(t, ok)
	assert.Equal(t, ".cloche/prompts/implement.md", path)

	_, ok = ResolveFile("You are a coding assistant.")
	assert.False(t, ok)

	_, ok = ResolveFile("")
	assert.False(t, ok)
}

func TestGitRevision(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("v1"), 0644))
	runGit(t, dir, "init")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "v1")
	firstRev := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	// Sleep isn't needed: git commit timestamps come from the clock at
	// commit time, and --until is inclusive to the second, so pause briefly
	// to guarantee the two commits land in different seconds.
	time.Sleep(1100 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("v2"), 0644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "v2")
	secondRev := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	assert.Equal(t, secondRev, GitRevision(dir, "prompt.md"))

	firstCommitTime, err := time.Parse(time.RFC3339, strings.TrimSpace(runGit(t, dir, "log", "-1", "--format=%aI", firstRev)))
	require.NoError(t, err)
	assert.Equal(t, firstRev, GitRevisionAt(dir, "prompt.md", firstCommitTime))

	assert.Equal(t, "", GitRevision(dir, "does-not-exist.md"))
	assert.Equal(t, "", GitRevision(t.TempDir(), "prompt.md"))
}

func TestResolveWorkflowPromptFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	workflow := `workflow develop {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step verify {
    run = "echo ok"
    results = [success, fail]
  }

  implement:success -> verify
  implement:fail -> abort
  verify:success -> done
  verify:fail -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(workflow), 0644))

	files := ResolveWorkflowPromptFiles(dir, "develop")
	require.Len(t, files, 1)
	assert.Equal(t, ".cloche/prompts/implement.md", files["implement"])

	assert.Nil(t, ResolveWorkflowPromptFiles(dir, "no-such-workflow"))
}
