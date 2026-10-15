package module_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/module"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

// makeRepo creates a git repo holding a module named "jira-tasks".
func makeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "module.toml"), "name = \"jira-tasks\"\nexports = [\"list-tasks\"]\n")
	write(t, filepath.Join(repo, "tasks.cloche"), wfSrc)
	write(t, filepath.Join(repo, "README.md"), "# Jira\n\n## Prerequisites\n\n- `jira` CLI\n- JIRA_TOKEN\n\n## Usage\n\nbind it.\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "v1")
	gitIn(t, repo, "tag", "v1.0.0")
	return repo
}

func TestParseSpec(t *testing.T) {
	s, err := module.ParseSpec("https://github.com/acme/x@v1.2.0")
	require.NoError(t, err)
	assert.Equal(t, module.Spec{URL: "https://github.com/acme/x", Ref: "v1.2.0"}, s)

	s, err = module.ParseSpec("git@github.com:acme/x.git")
	require.NoError(t, err)
	assert.Equal(t, module.Spec{URL: "git@github.com:acme/x.git"}, s)

	s, err = module.ParseSpec("git@github.com:acme/x.git@main")
	require.NoError(t, err)
	assert.Equal(t, module.Spec{URL: "git@github.com:acme/x.git", Ref: "main"}, s)

	s, err = module.ParseSpec("../shared/my-review")
	require.NoError(t, err)
	assert.Equal(t, "../shared/my-review", s.Path)
}

func TestAddListUpdateRemove_Git(t *testing.T) {
	repo := makeRepo(t)
	proj := t.TempDir()

	res, err := module.Add(proj, module.Spec{URL: repo, Ref: "v1.0.0"})
	require.NoError(t, err)
	assert.Equal(t, "jira-tasks", res.Entry.Name)
	assert.Equal(t, "v1.0.0", res.Entry.Ref)
	assert.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), res.Entry.Commit)
	assert.Contains(t, res.Prerequisites, "JIRA_TOKEN")
	assert.NotContains(t, res.Prerequisites, "bind it")

	vendored := filepath.Join(proj, ".cloche", "modules", "jira-tasks")
	assert.FileExists(t, filepath.Join(vendored, "module.toml"))
	assert.NoDirExists(t, filepath.Join(vendored, ".git"))
	hash, err := module.HashDir(vendored)
	require.NoError(t, err)
	assert.Equal(t, hash, res.Entry.Hash)

	entries, err := module.ReadLock(proj)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, res.Entry, entries[0])

	reg, err := module.Load(proj)
	require.NoError(t, err)
	assert.Contains(t, reg.Modules, "jira-tasks")

	_, err = module.Add(proj, module.Spec{URL: repo})
	assert.ErrorContains(t, err, "already installed")

	// New upstream commit; the pinned tag hasn't moved so update is a no-op,
	// but moving to HEAD shows a diff.
	write(t, filepath.Join(repo, "README.md"), "# Jira v2\n")
	gitIn(t, repo, "commit", "-qam", "v2")
	plan, err := module.PlanUpdate(proj, "jira-tasks", "")
	require.NoError(t, err)
	assert.Empty(t, plan.Diff)
	plan.Close()

	plan, err = module.PlanUpdate(proj, "jira-tasks", "main")
	require.NoError(t, err)
	assert.Contains(t, plan.Diff, "+# Jira v2")
	assert.False(t, plan.LocalEdit)
	// Planning changes nothing.
	b, _ := os.ReadFile(filepath.Join(vendored, "README.md"))
	assert.Contains(t, string(b), "Prerequisites")
	require.NoError(t, plan.Apply(proj))
	plan.Close()
	b, _ = os.ReadFile(filepath.Join(vendored, "README.md"))
	assert.Equal(t, "# Jira v2\n", string(b))
	entries, _ = module.ReadLock(proj)
	assert.Equal(t, "main", entries[0].Ref)
	assert.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), entries[0].Commit)

	// Local edits are flagged.
	write(t, filepath.Join(vendored, "README.md"), "edited")
	plan, err = module.PlanUpdate(proj, "jira-tasks", "")
	require.NoError(t, err)
	assert.True(t, plan.LocalEdit)
	plan.Close()

	e, err := module.Remove(proj, "jira-tasks")
	require.NoError(t, err)
	assert.Equal(t, "jira-tasks", e.Name)
	assert.NoDirExists(t, vendored)
	assert.NoFileExists(t, filepath.Join(proj, ".cloche", "modules.lock"))
	_, err = module.Remove(proj, "jira-tasks")
	assert.Error(t, err)
}

func TestAdd_LocalPathUsedInPlace(t *testing.T) {
	proj := t.TempDir()
	ext := filepath.Join(t.TempDir(), "my-review")
	write(t, filepath.Join(ext, "module.toml"), "name = \"my-review\"\nexports = [\"list-tasks\"]\n")
	write(t, filepath.Join(ext, "a.cloche"), wfSrc)

	res, err := module.Add(proj, module.Spec{Path: ext})
	require.NoError(t, err)
	p, ok := res.Entry.LocalPath()
	require.True(t, ok)
	assert.Equal(t, filepath.ToSlash(mustRel(t, proj, ext)), p)
	assert.NoDirExists(t, filepath.Join(proj, ".cloche", "modules", "my-review"))

	reg, err := module.Load(proj)
	require.NoError(t, err)
	assert.True(t, reg.Modules["my-review"].Local)

	_, err = module.PlanUpdate(proj, "my-review", "")
	assert.ErrorContains(t, err, "nothing to update")

	_, err = module.Remove(proj, "my-review")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(ext, "module.toml"), "local dir must survive remove")
}

func mustRel(t *testing.T, base, target string) string {
	t.Helper()
	r, err := filepath.Rel(base, target)
	require.NoError(t, err)
	return r
}

func TestAdd_RejectsSymlinksAndBadRef(t *testing.T) {
	repo := makeRepo(t)
	proj := t.TempDir()
	_, err := module.Add(proj, module.Spec{URL: repo, Ref: "nope"})
	assert.ErrorContains(t, err, "cannot resolve")

	require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(repo, "link")))
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "link")
	_, err = module.Add(proj, module.Spec{URL: repo})
	assert.ErrorContains(t, err, "symlink")
	assert.NoDirExists(t, filepath.Join(proj, ".cloche", "modules", "jira-tasks"))
}
