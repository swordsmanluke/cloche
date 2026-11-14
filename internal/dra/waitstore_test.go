package dra

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A wait saved by one store is found by a fresh one that is only told which
// projects exist: that is a daemon restart.
func TestFileWaitStore_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s1 := NewFileWaitStore(nil)
	w := Wait{ID: "w-7", DRA: "qa", Project: dir, CreatedAt: time.Now(), WaitRequest: WaitRequest{Poll: "p", Note: "n", Interval: time.Minute, Timeout: time.Hour}}
	require.NoError(t, s1.Save(w))
	w.LastOutput = "updated"
	require.NoError(t, s1.Save(w))

	s2 := NewFileWaitStore(func() []string { return []string{dir} })
	all, err := s2.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "updated", all[0].LastOutput)
	assert.Equal(t, time.Hour, all[0].Timeout)

	require.NoError(t, s2.Delete("w-7"))
	all, err = NewFileWaitStore(func() []string { return []string{dir} }).All()
	require.NoError(t, err)
	assert.Empty(t, all)
}

// An unreadable waits file is an error, not an empty store that the next save
// would overwrite.
func TestFileWaitStore_CorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir+"/.cloche/runs", 0o755))
	require.NoError(t, os.WriteFile(WaitsPath(dir), []byte("{not json"), 0o644))
	_, err := NewFileWaitStore(func() []string { return []string{dir} }).All()
	assert.Error(t, err)
}
