package spill

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLines_SpillsAnOversizedLineAndKeepsReading(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 5*1024*1024)
	in := "first\n" + big + "\n" + `{"type":"result","result":"CLOCHE_RESULT:success"}` + "\nlast"

	var got []string
	require.NoError(t, Lines(strings.NewReader(in), 1024*1024, dir, "plan", func(l []byte) { got = append(got, string(l)) }))

	require.Len(t, got, 4)
	assert.Equal(t, "first", got[0])
	var stub Stub
	require.NoError(t, json.Unmarshal([]byte(got[1]), &stub))
	assert.Equal(t, "cloche_spilled", stub.Type)
	assert.Equal(t, int64(len(big)), stub.Bytes)
	data, err := os.ReadFile(stub.Path)
	require.NoError(t, err)
	assert.Equal(t, big, string(data))
	assert.Contains(t, got[2], "CLOCHE_RESULT:success")
	assert.Equal(t, "last", got[3])
}

func TestLines_ShortLinesPassThrough(t *testing.T) {
	var got []string
	require.NoError(t, Lines(strings.NewReader("a\nb\n"), 10, t.TempDir(), "s", func(l []byte) { got = append(got, string(l)) }))
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestLines_SpillFailureDropsTheLineButDrains(t *testing.T) {
	var got []string
	in := strings.Repeat("y", 100) + "\nafter\n"
	// A file where the spill dir should be: MkdirAll fails.
	f := t.TempDir() + "/file"
	require.NoError(t, os.WriteFile(f, nil, 0644))
	require.NoError(t, Lines(strings.NewReader(in), 10, f, "s", func(l []byte) { got = append(got, string(l)) }))
	require.Len(t, got, 2)
	assert.Contains(t, got[0], `"path":""`)
	assert.Equal(t, "after", got[1])
}
