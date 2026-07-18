package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cloche-dev/cloche/internal/mcpauth"
	"github.com/cloche-dev/cloche/internal/version"
)

// AskHelpFunc handles an ask_user MCP tool call for an authenticated run.
// The daemon resolves task/attempt/channel info from runID itself rather
// than trusting client-supplied identifiers.
type AskHelpFunc func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (answer, respThreadID string, parked bool, err error)

// WithHelpMCP enables the /mcp endpoint's ask_user tool. secret is the
// daemon-generated HMAC key used to validate per-run bearer tokens minted at
// container start (see docker.Runtime.SetMCPSecret); fn performs the ask.
func WithHelpMCP(secret []byte, fn AskHelpFunc) HandlerOption {
	return func(h *Handler) {
		h.mcpSecret = secret
		h.askHelpFn = fn
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpErrorObj    `json:"error,omitempty"`
}

type mcpErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func mcpResult(id json.RawMessage, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpError(id json.RawMessage, code int, msg string) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpErrorObj{Code: code, Message: msg}}
}

// mcpToolError reports a tool-execution failure via isError, per MCP
// convention, rather than a JSON-RPC protocol-level error, so the calling
// agent sees the failure as tool output it can react to.
func mcpToolError(id json.RawMessage, msg string) mcpResponse {
	return mcpResult(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	})
}

var askUserToolSchema = map[string]any{
	"name":        "ask_user",
	"description": "Ask the user a question and block until they reply. Use for mid-task clarification, blocked decisions, or anything requiring human input. The question and reply are visible in `cloche threads`.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question":  map[string]any{"type": "string", "description": "The question to ask the user."},
			"title":     map[string]any{"type": "string", "description": "Optional short summary; defaults to the first line of question."},
			"options":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional suggested answers."},
			"thread_id": map[string]any{"type": "string", "description": "Optional; continue an existing thread from a prior ask_user call."},
			"ask_key":   map[string]any{"type": "string", "description": "Optional idempotency key."},
		},
		"required": []string{"question"},
	},
}

// handleMCP serves the ask_user MCP tool over HTTP JSON-RPC (the
// "single JSON response" variant of the Streamable HTTP transport). The
// caller authenticates as a specific run via ?run_id=<id> plus a bearer
// token minted at container start (see mcpToken).
func (h *Handler) handleMCP(w http.ResponseWriter, r *http.Request) {
	if h.askHelpFn == nil || len(h.mcpSecret) == 0 {
		http.Error(w, "help channel is not enabled on this daemon", http.StatusNotFound)
		return
	}

	runID := r.URL.Query().Get("run_id")
	if runID == "" || !mcpauth.Valid(h.mcpSecret, runID, bearerToken(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}

	// Notifications (no "id") get no response body, per JSON-RPC/MCP.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := h.dispatchMCP(r.Context(), runID, req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) dispatchMCP(ctx context.Context, runID string, req mcpRequest) mcpResponse {
	switch req.Method {
	case "initialize":
		return mcpResult(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cloche-help", "version": version.Version()},
		})
	case "ping":
		return mcpResult(req.ID, map[string]any{})
	case "tools/list":
		return mcpResult(req.ID, map[string]any{"tools": []any{askUserToolSchema}})
	case "tools/call":
		return h.handleMCPToolCall(ctx, runID, req)
	default:
		return mcpError(req.ID, -32601, "method not found: "+req.Method)
	}
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type askUserArgs struct {
	Question string   `json:"question"`
	Title    string   `json:"title"`
	Options  []string `json:"options"`
	ThreadID string   `json:"thread_id"`
	AskKey   string   `json:"ask_key"`
}

func (h *Handler) handleMCPToolCall(ctx context.Context, runID string, req mcpRequest) mcpResponse {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return mcpError(req.ID, -32602, "invalid params")
	}
	if params.Name != "ask_user" {
		return mcpError(req.ID, -32602, "unknown tool: "+params.Name)
	}

	var args askUserArgs
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return mcpError(req.ID, -32602, "invalid arguments")
		}
	}
	if strings.TrimSpace(args.Question) == "" {
		return mcpToolError(req.ID, "question is required")
	}

	answer, threadID, parked, err := h.askHelpFn(ctx, runID, args.Question, args.Title, args.ThreadID, args.Options, args.AskKey)
	if err != nil {
		return mcpToolError(req.ID, err.Error())
	}

	text := answer
	if parked {
		text = "No answer yet. The run is being parked; you will receive the answer when it resumes."
	}
	return mcpResult(req.ID, map[string]any{
		"content":  []map[string]any{{"type": "text", "text": text}},
		"isError":  false,
		"threadId": threadID,
		"parked":   parked,
	})
}
