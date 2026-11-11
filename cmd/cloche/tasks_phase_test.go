package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noFile(string) ([]byte, error) { return nil, errors.New("no file") }

func TestIsTrackerSubcommand(t *testing.T) {
	assert.True(t, isTrackerSubcommand([]string{"create", "--title", "x"}))
	assert.True(t, isTrackerSubcommand([]string{"update", "t-1"}))
	assert.True(t, isTrackerSubcommand([]string{"find"}))
	assert.False(t, isTrackerSubcommand(nil))
	assert.False(t, isTrackerSubcommand([]string{"--project", "/x"}))
}

func TestParseTrackerArgs_Create(t *testing.T) {
	req, err := parseTrackerArgs([]string{"create", "--title", "Red main", "--body=details", "--priority", "0",
		"--label", "urgent", "--label", "ci", "--source", "ci", "--key", "main-red", "--project", "/p"}, noFile)
	require.NoError(t, err)
	assert.Equal(t, "create", req.Path)
	assert.Equal(t, "/p", req.ProjectDir)
	assert.Equal(t, map[string]string{
		"title": "Red main", "body": "details", "priority": "0", "labels": "urgent,ci",
		"signal_source": "ci", "signal_key": "main-red",
	}, req.Inputs)
}

func TestParseTrackerArgs_CreateBodyFile(t *testing.T) {
	req, err := parseTrackerArgs([]string{"create", "--title", "t", "--body-file", "b.md"},
		func(p string) ([]byte, error) { assert.Equal(t, "b.md", p); return []byte("from file"), nil })
	require.NoError(t, err)
	assert.Equal(t, "from file", req.Inputs["body"])
}

func TestParseTrackerArgs_CreateErrors(t *testing.T) {
	for _, args := range [][]string{
		{"create"},
		{"create", "--title", "t", "--key", "k"},
		{"create", "--title", "t", "--comment", "c"},
		{"create", "--title", "t", "extra"},
		{"create", "--title"},
		{"create", "--title", "t", "--nope", "x"},
	} {
		_, err := parseTrackerArgs(args, noFile)
		assert.Error(t, err, strings.Join(args, " "))
	}
}

func TestParseTrackerArgs_Update(t *testing.T) {
	req, err := parseTrackerArgs([]string{"update", "t-1", "--comment", "again", "--priority", "1",
		"--add-label", "a", "--add-label", "b", "--remove-label", "c"}, noFile)
	require.NoError(t, err)
	assert.Equal(t, "update", req.Path)
	assert.Equal(t, map[string]string{
		"action": "update", "target_task_id": "t-1", "comment": "again", "priority": "1",
		"add_labels": "a,b", "remove_labels": "c",
	}, req.Inputs)

	for _, args := range [][]string{
		{"update"},
		{"update", "t-1"},
		{"update", "--comment", "c"},
		{"update", "t-1", "--title", "x"},
	} {
		_, err := parseTrackerArgs(args, noFile)
		assert.Error(t, err, strings.Join(args, " "))
	}
}

func TestParseTrackerArgs_Find(t *testing.T) {
	req, err := parseTrackerArgs([]string{"find", "--source", "ci", "--key", "k"}, noFile)
	require.NoError(t, err)
	assert.Equal(t, "update", req.Path)
	assert.Equal(t, map[string]string{"action": "find", "signal_source": "ci", "signal_key": "k"}, req.Inputs)

	_, err = parseTrackerArgs([]string{"find", "--source", "ci"}, noFile)
	assert.Error(t, err)
}

func TestPostTrackerPhase(t *testing.T) {
	var gotPath, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		if strings.Contains(gotBody, "boom") {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"create-task failed: x"}`))
			return
		}
		w.Write([]byte(`{"outputs":{"new_task_id":"t-9"}}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	out, err := postTrackerPhase(addr, &trackerRequest{Path: "create", ProjectDir: "/home/p", Inputs: map[string]string{"title": "x"}}, "tok")
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "t-9", out["new_task_id"])
	assert.Equal(t, "/api/projects/%2Fhome%2Fp/tasks/create", gotPath)
	assert.Contains(t, gotBody, `"title":"x"`)

	_, err = postTrackerPhase(addr, &trackerRequest{Path: "create", ProjectDir: "/p", Inputs: map[string]string{"title": "boom"}}, "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create-task failed")
}
