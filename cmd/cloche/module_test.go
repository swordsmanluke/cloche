package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleCommand_Lifecycle(t *testing.T) {
	repo := t.TempDir()
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	run(repo, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "module.toml"), []byte("name = \"mod-a\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("# A\n## Prerequisites\nneeds jq\n"), 0o644))
	run(repo, "add", "-A")
	run(repo, "commit", "-qm", "init")

	proj := t.TempDir()
	var out bytes.Buffer
	require.NoError(t, moduleCommand([]string{"add", "file://" + repo, "--project", proj}, nil, &out))
	assert.Contains(t, out.String(), "needs jq")

	out.Reset()
	require.NoError(t, moduleCommand([]string{"list", "-p", proj}, nil, &out))
	assert.Contains(t, out.String(), "mod-a")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("# B\n"), 0o644))
	run(repo, "commit", "-qam", "two")

	// Declining leaves the vendored copy alone.
	out.Reset()
	require.NoError(t, moduleCommand([]string{"update", "mod-a", "-p", proj}, strings.NewReader("n\n"), &out))
	assert.Contains(t, out.String(), "+# B")
	assert.Contains(t, out.String(), "Aborted")
	b, _ := os.ReadFile(filepath.Join(proj, ".cloche", "modules", "mod-a", "README.md"))
	assert.Contains(t, string(b), "needs jq")

	out.Reset()
	require.NoError(t, moduleCommand([]string{"update", "mod-a", "-p", proj}, strings.NewReader("y\n"), &out))
	b, _ = os.ReadFile(filepath.Join(proj, ".cloche", "modules", "mod-a", "README.md"))
	assert.Equal(t, "# B\n", string(b))

	require.NoError(t, moduleCommand([]string{"remove", "mod-a", "-p", proj}, nil, &out))
	assert.Error(t, moduleCommand([]string{"bogus"}, nil, &out))
}
