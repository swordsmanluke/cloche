package docker_test

import (
	"context"
	"os/exec"
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

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   t.TempDir(),
		Cmd:          []string{"sleep", "30"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)

	err = rt.Stop(ctx, containerID)
	assert.NoError(t, err)
}

func TestDockerRuntime_Wait(t *testing.T) {
	skipIfNoDocker(t)

	rt, err := docker.NewRuntime()
	require.NoError(t, err)

	ctx := context.Background()
	containerID, err := rt.Start(ctx, ports.ContainerConfig{
		Image:        "alpine:latest",
		WorkflowName: "test",
		ProjectDir:   t.TempDir(),
		Cmd:          []string{"echo", "hello"},
	})
	require.NoError(t, err)

	exitCode, err := rt.Wait(ctx, containerID)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}
