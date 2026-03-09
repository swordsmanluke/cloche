package logstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseClaudeLine_TextDelta(t *testing.T) {
	line := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}`
	text, ok := ParseClaudeLine([]byte(line))
	assert.True(t, ok)
	assert.Equal(t, "Hello!", text)
}

func TestParseClaudeLine_ToolUse(t *testing.T) {
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

func TestParseClaudeStream_ToolUse(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me read the file."}}
{"type":"content_block_stop","index":0}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"Read","input":{}}}
{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"/foo\"}"}}
{"type":"content_block_stop","index":1}
{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"The file contains..."}}
{"type":"content_block_stop","index":2}
`
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "Let me read the file.")
	assert.Contains(t, result, "--- Tool: Read ---")
	assert.Contains(t, result, "The file contains...")
}

func TestParseClaudeStream_ResultEvent(t *testing.T) {
	input := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done.\n"}}
{"type":"content_block_stop","index":0}
{"type":"result","subtype":"success","result":"Done.\nCLOCHE_RESULT:success","cost_usd":0.05,"duration_ms":1234,"duration_api_ms":1000}
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

func TestParseClaudeStream_MixedContent(t *testing.T) {
	// Non-JSON lines should be preserved
	input := "some preamble\n{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n"
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "some preamble")
	assert.Contains(t, result, "hello")
}

func TestParseClaudeStream_Empty(t *testing.T) {
	result := ParseClaudeStream([]byte(""))
	assert.Equal(t, "", string(result))
}

func TestParseClaudeStream_MultipleToolCalls(t *testing.T) {
	input := `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"Bash","input":{}}}
{"type":"content_block_stop","index":0}
{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Running tests..."}}
{"type":"content_block_stop","index":1}
{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t2","name":"Edit","input":{}}}
{"type":"content_block_stop","index":2}
`
	result := string(ParseClaudeStream([]byte(input)))
	assert.Contains(t, result, "--- Tool: Bash ---")
	assert.Contains(t, result, "Running tests...")
	assert.Contains(t, result, "--- Tool: Edit ---")
}
