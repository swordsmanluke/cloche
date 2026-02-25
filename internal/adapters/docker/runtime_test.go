package docker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker not available, skipping integration test")
	}
}

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available, skipping integration test")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "test"},
		{"git", "config", "user.email", "test@test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git setup cmd %v failed: %s", args, out)
	}
}

func TestFindFreePort(t *testing.T) {
	port1, err := docker.FindFreePort()
	require.NoError(t, err)
	assert.Greater(t, port1, 0)

	port2, err := docker.FindFreePort()
	require.NoError(t, err)
	assert.Greater(t, port2, 0)
	assert.NotEqual(t, port1, port2, "two calls should return different ports")
}

func TestDockerRuntime_StartAndStop(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoGit(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	initGitRepo(t, dir)

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   dir,
		RunID:        "test-run-1",
		Cmd:          []string{"sleep", "30"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)

	err = rt.Stop(ctx, containerID)
	assert.NoError(t, err)
}

func TestDockerRuntime_Wait(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoGit(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	initGitRepo(t, dir)

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   dir,
		RunID:        "test-run-2",
		Cmd:          []string{"echo", "hello"},
	})
	require.NoError(t, err)

	exitCode, err := rt.Wait(ctx, containerID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestDockerRuntime_FilesPresent(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoGit(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	initGitRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "testfile.txt"), []byte("hello"), 0644))

	ctx := context.Background()
	// Run cat on the copied file to verify it exists in container
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   dir,
		RunID:        "test-run-3",
		Cmd:          []string{"cat", "/workspace/testfile.txt"},
	})
	require.NoError(t, err)

	exitCode, err := rt.Wait(ctx, containerID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode, "cat should succeed — file was copied into container")
}
