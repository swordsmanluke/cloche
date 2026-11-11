package docker_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/docker"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// notesRuntime serves the notes the "agent" wrote when copied out.
type notesRuntime struct {
	fakeRuntime
	notes string
}

func (n *notesRuntime) CopyFrom(_ context.Context, _, _, dst string) error {
	return os.WriteFile(dst, []byte(n.notes), 0o644)
}

// notes.md comes back out of the DRA container to the project, also when the
// run failed (the container is then stopped but kept).
func TestDRANotesCopiedOutEvenWhenRunFails(t *testing.T) {
	for _, succeeded := range []bool{true, false} {
		root := t.TempDir()
		notesRel := ".cloche/dras/auth/notes.md"
		notesPath := filepath.Join(root, filepath.FromSlash(notesRel))
		require.NoError(t, os.MkdirAll(filepath.Dir(notesPath), 0o755))
		require.NoError(t, os.WriteFile(notesPath, []byte("old notes"), 0o644))

		rt := &notesRuntime{notes: "new notes from the wake-up"}
		pool := docker.NewContainerPool(rt)
		go func() {
			time.Sleep(20 * time.Millisecond)
			pool.NotifyReady(rt.lastStarted())
		}()
		_, err := pool.SessionFor(context.Background(), "att", ports.ContainerConfig{
			Image: "img", HostProjectDir: root, ProjectDir: root,
			Locked: docker.DRAProfile(notesRel),
		})
		require.NoError(t, err)

		require.NoError(t, pool.CleanupAttempt(context.Background(), "att", false, succeeded))
		got, err := os.ReadFile(notesPath)
		require.NoError(t, err)
		assert.Equal(t, "new notes from the wake-up", string(got), "succeeded=%v", succeeded)
	}
}
