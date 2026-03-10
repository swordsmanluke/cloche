package logstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- Claude Code JSON format (type: "assistant", "system", "user") ---

func TestParseClaudeLine_AssistantText(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll read the file."}]}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "I'll read the file.")
}

func TestParseClaudeLine_AssistantToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check."},{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/main.go"}}]}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "Let me check.")
	assert.Contains(t, text, "--- Tool: Read('/workspace/main.go') ---")
}

func TestParseClaudeLine_AssistantToolOnly(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -al"}}]}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "--- Tool: Bash('ls -al') ---")
}

func TestParseClaudeLine_AssistantToolNoInput(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "--- Tool: Bash ---")
}

func TestParseClaudeLine_AssistantEmptyContent(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[]}}`
	_, ok := ParseClaudeLine([]byte(line))
	assert.False(t, ok)
}

func TestParseClaudeLine_SystemEvent(t *testing.T) {
	line := `{"type":"system","subtype":"init","cwd":"/workspace"}`
	_, ok := ParseClaudeLine([]byte(line))
	assert.False(t, ok)
}

func TestParseClaudeLine_UserEvent(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	_, ok := ParseClaudeLine([]byte(line))
	assert.False(t, ok)
}

// --- Streaming API format (content_block_delta, content_block_start) ---

func TestParseClaudeLine_TextDelta(t *testing.T) {
	line := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Equal(t, "Hello!", text)
}

func TestParseClaudeLine_ToolUseBlockStart(t *testing.T) {
	line := `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"Read","input":{}}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	// content_block_start typically has empty input, so just tool name
	assert.Contains(t, text, "--- Tool: Read ---")
}

func TestParseClaudeLine_ToolUseBlockStartWithInput(t *testing.T) {
	line := `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/workspace/go.mod"}}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "--- Tool: Read('/workspace/go.mod') ---")
}

func TestParseClaudeLine_ProtocolEvent(t *testing.T) {
	line := `{"type":"message_start","message":{"id":"msg_01"}}`
	_, ok := ParseClaudeLine([]byte(line))
	assert.False(t, ok)
}

func TestParseClaudeLine_NonJSON(t *testing.T) {
	text, ok := ParseClaudeLine([]byte("plain text"))
	assert.True(t, ok)
	assert.Equal(t, "plain text", text)
}

func TestParseClaudeLine_Empty(t *testing.T) {
	_, ok := ParseClaudeLine([]byte(""))
	assert.False(t, ok)
}

// --- formatToolCall ---

func TestFormatToolCall_Bash(t *testing.T) {
	input := json.RawMessage(`{"command":"go test ./...","description":"Run tests"}`)
	assert.Equal(t, "Bash('go test ./...')", formatToolCall("Bash", input))
}

func TestFormatToolCall_Read(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/workspace/main.go"}`)
	assert.Equal(t, "Read('/workspace/main.go')", formatToolCall("Read", input))
}

func TestFormatToolCall_Edit(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/workspace/main.go","old_string":"foo","new_string":"bar"}`)
	assert.Equal(t, "Edit('/workspace/main.go')", formatToolCall("Edit", input))
}

func TestFormatToolCall_Glob(t *testing.T) {
	input := json.RawMessage(`{"pattern":"**/*.go"}`)
	assert.Equal(t, "Glob('**/*.go')", formatToolCall("Glob", input))
}

func TestFormatToolCall_Grep(t *testing.T) {
	input := json.RawMessage(`{"pattern":"func main","path":"/workspace"}`)
	assert.Equal(t, "Grep('func main')", formatToolCall("Grep", input))
}

func TestFormatToolCall_Agent(t *testing.T) {
	input := json.RawMessage(`{"description":"Find test files","prompt":"Search for test files"}`)
	assert.Equal(t, "Agent('Find test files')", formatToolCall("Agent", input))
}

func TestFormatToolCall_NilInput(t *testing.T) {
	assert.Equal(t, "Bash", formatToolCall("Bash", nil))
}

func TestFormatToolCall_EmptyInput(t *testing.T) {
	input := json.RawMessage(`{}`)
	assert.Equal(t, "Read", formatToolCall("Read", input))
}

func TestFormatToolCall_UnknownToolWithCommand(t *testing.T) {
	input := json.RawMessage(`{"command":"do-something"}`)
	assert.Equal(t, "CustomTool('do-something')", formatToolCall("CustomTool", input))
}

func TestFormatToolCall_UnknownToolNoKnownParam(t *testing.T) {
	input := json.RawMessage(`{"foo":"bar"}`)
	assert.Equal(t, "CustomTool", formatToolCall("CustomTool", input))
}

func TestFormatToolCall_LongCommandTruncated(t *testing.T) {
	long := strings.Repeat("x", 100)
	input := json.RawMessage(`{"command":"` + long + `"}`)
	result := formatToolCall("Bash", input)
	assert.Contains(t, result, "...")
	// 80 chars + "..." = truncated
	assert.Equal(t, fmt.Sprintf("Bash('%s...')", long[:80]), result)
}

// --- ParseClaudeStream ---

func TestParseClaudeStream_AssistantMessages(t *testing.T) {
	input := `{"type":"system","subtype":"init","cwd":"/workspace"}
{"type":"assistant","message":{"content":[{"type":"text","text":"I'll implement the feature."}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/workspace/main.go","old_string":"foo","new_string":"bar"}},{"type":"text","text":"Done editing."}]}}
`
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "I'll implement the feature.")
	assert.Contains(t, result, "--- Tool: Edit('/workspace/main.go') ---")
	assert.Contains(t, result, "Done editing.")
	assert.NotContains(t, result, "system")
	assert.NotContains(t, result, "tool_result")
}

func TestParseClaudeStream_TextDeltas(t *testing.T) {
	input := `{"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant"}}
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello, "}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world!"}}
{"type":"content_block_stop","index":0}
{"type":"message_delta","delta":{"stop_reason":"end_turn"}}
{"type":"message_stop"}
`
	result := ParseClaudeStream([]byte(input))
	assert.Equal(t, "Hello, world!", string(result))
}

func TestParseClaudeStream_ResultEvent(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done.\n"}}
{"type":"result","subtype":"success","result":"Done.\nCLOCHE_RESULT:success"}
`
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "Done.\n")
	assert.Contains(t, result, "CLOCHE_RESULT:success")
}

func TestParseClaudeStream_NonJSON(t *testing.T) {
	input := "This is plain text output\nfrom a script step\n"
	result := ParseClaudeStream([]byte(input))
	assert.Equal(t, input, string(result))
}

func TestParseClaudeStream_Empty(t *testing.T) {
	result := ParseClaudeStream([]byte(""))
	assert.Equal(t, "", string(result))
}
