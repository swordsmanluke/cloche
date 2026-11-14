package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/mcpauth"
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

// fakeAskHelp returns an AskHelpFunc standing in for the help router. It
// blocks until release is closed by the test (the scaled "park_after" instant) or ctx is cancelled — the same contract as help.Router.Ask — and then
// reports parked=true.
func fakeAskHelp(release <-chan struct{}) AskHelpFunc {
	return func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		select {
		case <-release:
			return "", "thread-park", true, nil
		case <-ctx.Done():
			return "", "", false, ctx.Err()
		}
	}
}

// TestMCP_ToolsCall_AskUser_OutlastsClientWindow is the regression test for
// wrapped_cloche-b79: an unanswered ask_user over /mcp that outlasts the
// client's silence window (~60s in real life) must still end in a parked
// result. Times are scaled (window=200ms stands in for 60s, park_after=800ms
// for 5 minutes). The client aborts if the stream is silent for a full window,
// like Claude's MCP client; the server keeps the stream alive with SSE
// keepalives until fakeAskHelp reports parked=true.
func TestMCP_ToolsCall_AskUser_OutlastsClientWindow(t *testing.T) {
	const clientWindow = 200 * time.Millisecond // scaled "60s"

	release := make(chan struct{}) // closed when the scaled park_after elapses
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	secret := []byte("test-secret")
	h, err := NewHandler(store, store, WithHelpMCP(secret, fakeAskHelp(release)), WithMCPKeepalive(clientWindow/4))
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	defer srv.Close()

	go func() {
		time.Sleep(4 * clientWindow)
		close(release)
	}()

	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "tools/call",
		"params": map[string]any{"name": "ask_user", "arguments": map[string]any{"question": "Q?"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/mcp?run_id=run-1", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+mcpauth.Token(secret, "run-1"))
	req.Header.Set("Accept", "application/json, text/event-stream")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Idle-timeout client: abort if no bytes arrive for a whole window.
	var body bytes.Buffer
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
loop:
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				break loop
			}
			body.Write(c)
		case <-time.After(clientWindow):
			t.Fatalf("client saw %v of silence: ask would time out before parking (body so far %q)", clientWindow, body.String())
		}
	}
	assert.Greater(t, time.Since(start), 3*clientWindow, "ask must outlast the client window")

	var final struct {
		Result struct {
			IsError  bool   `json:"isError"`
			Parked   bool   `json:"parked"`
			ThreadID string `json:"threadId"`
		} `json:"result"`
	}
	idx := strings.LastIndex(body.String(), "data: ")
	require.GreaterOrEqual(t, idx, 0, body.String())
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(body.String()[idx+6:])), &final))
	assert.True(t, final.Result.Parked)
	assert.False(t, final.Result.IsError)
	assert.Equal(t, "thread-park", final.Result.ThreadID)
}

// A client that does not accept SSE keeps getting the plain JSON response.
func TestMCP_ToolsCall_NoSSEAccept_PlainJSON(t *testing.T) {
	h, secret := setupMCPHandler(t, func(ctx context.Context, runID, q, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		return "ans", "t1", false, nil
	})
	w := mcpPost(h, "/mcp?run_id=run-1", mcpauth.Token(secret, "run-1"), map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ask_user", "arguments": map[string]any{"question": "Q?"}},
	})
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}
