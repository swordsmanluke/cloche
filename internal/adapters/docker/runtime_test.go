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

func TestDockerRuntime_StartAndStop(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))

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
	defer rt.Remove(ctx, containerID)

	err = rt.Stop(ctx, containerID)
	assert.NoError(t, err)
}

func TestDockerRuntime_Wait(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   dir,
		RunID:        "test-run-2",
		Cmd:          []string{"echo", "hello"},
	})
	require.NoError(t, err)
	defer rt.Remove(ctx, containerID)

	exitCode, err := rt.Wait(ctx, containerID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestDockerRuntime_FilesPresent(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
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
	defer rt.Remove(ctx, containerID)

	exitCode, err := rt.Wait(ctx, containerID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode, "cat should succeed — file was copied into container")
}
