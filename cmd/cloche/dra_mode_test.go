package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDRAModeClient_FromEnv(t *testing.T) {
	t.Setenv("CLOCHE_DRA_URL", "")
	t.Setenv("CLOCHE_DRA_TOKEN", "")
	assert.Nil(t, draModeClient())
	t.Setenv("CLOCHE_DRA_URL", "http://h:1/")
	assert.Nil(t, draModeClient(), "needs both")
	t.Setenv("CLOCHE_DRA_TOKEN", "tok")
	c := draModeClient()
	require.NotNil(t, c)
	assert.Equal(t, "http://h:1", c.base)
}

func TestDRAMode_UsesCredentialAndDRAEndpoints(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		json.NewEncoder(w).Encode(map[string]any{"outputs": map[string]string{"new_task_id": "t-9"}})
	}))
	defer srv.Close()
	c := &draClient{base: srv.URL, token: "the-token"}

	handled, err := runDRACommand(c, []string{"tasks", "create", "--title", "x"})
	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, "Bearer the-token", gotAuth)
	assert.Equal(t, "/api/dra/tasks/create", gotPath)
	assert.Contains(t, gotBody, `"title":"x"`)

	_, err = runDRACommand(c, []string{"tasks", "close", "t-1"})
	require.NoError(t, err)
	assert.Equal(t, "/api/dra/tasks/close", gotPath)
	assert.JSONEq(t, `{"task_id":"t-1"}`, gotBody)

	_, err = runDRACommand(c, []string{"tasks", "update", "t-1", "--comment", "c"})
	require.NoError(t, err)
	assert.Equal(t, "/api/dra/tasks/update", gotPath)
}

func TestDRAMode_RefusesOtherCommands(t *testing.T) {
	c := &draClient{base: "http://127.0.0.1:1", token: "t"} // never contacted
	for _, cmd := range []string{"stop", "delete", "resume", "shutdown", "loop", "project", "signals", "run", "dashboard"} {
		handled, err := runDRACommand(c, []string{cmd, "x"})
		assert.True(t, handled, cmd)
		assert.Error(t, err, cmd)
	}
	for _, cmd := range []string{"help", "version", "get", "set"} {
		handled, _ := runDRACommand(c, []string{cmd})
		assert.False(t, handled, cmd)
	}
}
