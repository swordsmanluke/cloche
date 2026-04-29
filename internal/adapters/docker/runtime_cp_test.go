package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipDockerIfUnavailable(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}
}

// createScratchContainer creates a minimal stopped container and returns its
// ID. The caller is responsible for cleanup.
func createScratchContainer(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "create", "alpine:latest", "true").Output()
	require.NoError(t, err, "docker create failed")
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// TestCopyProjectToContainer_WalkErrorPropagates verifies that when
// writeTarFromProject fails (e.g. due to an unreadable file), the error is
// returned to the caller instead of a silent success.  The key behavior under
// test is that pipeW.CloseWithError(walkErr) is called rather than a clean
// tw.Close() + pipeW.Close(), so docker cp sees a broken stream.
func TestCopyProjectToContainer_WalkErrorPropagates(t *testing.T) {
	skipDockerIfUnavailable(t)
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 000 does not restrict access, cannot force a walk error")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0644))

	// Create a file and make it unreadable to force os.Open to fail during
	// the tar walk, triggering the CloseWithError path.
	badFile := filepath.Join(dir, "bad.txt")
	require.NoError(t, os.WriteFile(badFile, []byte("secret"), 0644))
	require.NoError(t, os.Chmod(badFile, 0000))
	t.Cleanup(func() { os.Chmod(badFile, 0644) }) // restore so t.TempDir cleanup works

	containerID := createScratchContainer(t)

	ctx := context.Background()
	// Use at least one ignore pattern so the slow (piped) path is exercised.
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	err := copyProjectToContainer(ctx, dir, containerID, patterns)

	assert.Error(t, err, "copyProjectToContainer must return an error when the tar walk fails")
	assert.Contains(t, err.Error(), "building tar archive",
		"error must identify that the tar build failed, not report success")
}

// TestCopyProjectToContainer_HappyPath verifies that a readable project is
// copied into the container successfully when ignore patterns are in use (the
// slow piped path) and no error is returned.
func TestCopyProjectToContainer_HappyPath(t *testing.T) {
	skipDockerIfUnavailable(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "world.txt"), []byte("world"), 0644))

	containerID := createScratchContainer(t)

	ctx := context.Background()
	patterns := []ignorePattern{{pattern: ".git", matchBase: true}}
	err := copyProjectToContainer(ctx, dir, containerID, patterns)
	assert.NoError(t, err)
}
