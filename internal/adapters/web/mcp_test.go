package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/mcpauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMCPHandler(t *testing.T, fn AskHelpFunc) (*Handler, []byte) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	secret := []byte("test-secret")
	h, err := NewHandler(store, store, WithHelpMCP(secret, fn))
	require.NoError(t, err)
	return h, secret
}

func mcpPost(h *Handler, url string, token string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", url, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestMCP_Disabled(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	h, err := NewHandler(store, store)
	require.NoError(t, err)

	w := mcpPost(h, "/mcp?run_id=run-1", "anything", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMCP_Unauthorized(t *testing.T) {
	h, _ := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		return "answer", "thread-1", false, nil
	})

	w := mcpPost(h, "/mcp?run_id=run-1", "wrong-token", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = mcpPost(h, "/mcp", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMCP_Initialize(t *testing.T) {
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		return "unused", "unused", false, nil
	})
	token := mcpauth.Token(secret, "run-1")

	w := mcpPost(h, "/mcp?run_id=run-1", token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp mcpResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

func TestMCP_Notification_NoBody(t *testing.T) {
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		return "unused", "unused", false, nil
	})
	token := mcpauth.Token(secret, "run-1")

	w := mcpPost(h, "/mcp?run_id=run-1", token, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestMCP_ToolsList(t *testing.T) {
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		return "unused", "unused", false, nil
	})
	token := mcpauth.Token(secret, "run-1")

	w := mcpPost(h, "/mcp?run_id=run-1", token, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Result.Tools, 1)
	assert.Equal(t, "ask_user", resp.Result.Tools[0].Name)
}

func TestMCP_ToolsCall_AskUser(t *testing.T) {
	var gotRunID, gotQuestion string
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		gotRunID = runID
		gotQuestion = question
		return "Use schema B", "thread-42", false, nil
	})
	token := mcpauth.Token(secret, "run-1")

	w := mcpPost(h, "/mcp?run_id=run-1", token, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "ask_user",
			"arguments": map[string]any{"question": "Which schema should I use?"},
		},
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "run-1", gotRunID)
	assert.Equal(t, "Which schema should I use?", gotQuestion)

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError  bool   `json:"isError"`
			ThreadID string `json:"threadId"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Result.Content, 1)
	assert.Equal(t, "Use schema B", resp.Result.Content[0].Text)
	assert.False(t, resp.Result.IsError)
	assert.Equal(t, "thread-42", resp.Result.ThreadID)
}

func TestMCP_ToolsCall_MissingQuestion(t *testing.T) {
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		t.Fatal("askHelpFn should not be called with an empty question")
		return "", "", false, nil
	})
	token := mcpauth.Token(secret, "run-1")

	w := mcpPost(h, "/mcp?run_id=run-1", token, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "ask_user", "arguments": map[string]any{}},
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Result.IsError)
}
