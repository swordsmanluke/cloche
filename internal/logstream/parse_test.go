package logstream

import (
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check."},{"type":"tool_use","name":"Read"}]}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Contains(t, text, "Let me check.")
	assert.Contains(t, text, "--- Tool: Read ---")
}

func TestParseClaudeLine_AssistantToolOnly(t *testing.T) {
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
	assert.Contains(t, text, "--- Tool: Read ---")
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

// --- ParseClaudeStream ---

func TestParseClaudeStream_AssistantMessages(t *testing.T) {
	input := `{"type":"system","subtype":"init","cwd":"/workspace"}
{"type":"assistant","message":{"content":[{"type":"text","text":"I'll implement the feature."}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit"},{"type":"text","text":"Done editing."}]}}
`
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "I'll implement the feature.")
	assert.Contains(t, result, "--- Tool: Edit ---")
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
