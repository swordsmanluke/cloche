package local_test

import (
	"context"
	"io"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalRuntime_StartAndWait(t *testing.T) {
	rt := local.NewRuntime("sh")

	id, err := rt.Start(context.Background(), ports.ContainerConfig{
		ProjectDir: t.TempDir(),
		Cmd:        []string{"sh", "-c", "echo hello; exit 0"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	exitCode, err := rt.Wait(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestLocalRuntime_AttachOutput(t *testing.T) {
	rt := local.NewRuntime("sh")

	id, err := rt.Start(context.Background(), ports.ContainerConfig{
		ProjectDir: t.TempDir(),
		Cmd:        []string{"sh", "-c", "echo hello-world"},
	})
	require.NoError(t, err)

	reader, err := rt.AttachOutput(context.Background(), id)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello-world")

	exitCode, err := rt.Wait(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestLocalRuntime_NonZeroExit(t *testing.T) {
	rt := local.NewRuntime("sh")

	id, err := rt.Start(context.Background(), ports.ContainerConfig{
		ProjectDir: t.TempDir(),
		Cmd:        []string{"sh", "-c", "exit 42"},
	})
	require.NoError(t, err)

	exitCode, err := rt.Wait(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, 42, exitCode)
}

func TestLocalRuntime_Stop(t *testing.T) {
	rt := local.NewRuntime("sh")

	id, err := rt.Start(context.Background(), ports.ContainerConfig{
		ProjectDir: t.TempDir(),
		Cmd:        []string{"sh", "-c", "sleep 60"},
	})
	require.NoError(t, err)

	err = rt.Stop(context.Background(), id)
	require.NoError(t, err)

	exitCode, err := rt.Wait(context.Background(), id)
	require.NoError(t, err)
	assert.NotEqual(t, 0, exitCode) // killed process exits non-zero
}

func TestLocalRuntime_NotFound(t *testing.T) {
	rt := local.NewRuntime("sh")

	_, err := rt.Wait(context.Background(), "nonexistent")
	assert.Error(t, err)

	err = rt.Stop(context.Background(), "nonexistent")
	assert.Error(t, err)

	_, err = rt.AttachOutput(context.Background(), "nonexistent")
	assert.Error(t, err)
}
