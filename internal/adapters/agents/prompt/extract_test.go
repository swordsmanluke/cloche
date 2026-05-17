package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractStreamText_AssistantTextBlock(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world\n"}]},"uuid":"abc-123"}`)
	got := extractStreamText(line)
	assert.Equal(t, "Hello world\n", got)
}

func TestExtractStreamText_AssistantToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/main.go"}}]},"uuid":"def-456"}`)
	got := extractStreamText(line)
	assert.Equal(t, "--- Tool: Read('/workspace/main.go') ---\n", got)
}

func TestExtractStreamText_AssistantMixed(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check that file."},{"type":"tool_use","name":"Grep","input":{"pattern":"TODO"}}]},"uuid":"ghi-789"}`)
	got := extractStreamText(line)
	assert.Contains(t, got, "Let me check that file.\n")
	assert.Contains(t, got, "--- Tool: Grep('TODO') ---\n")
}

func TestExtractStreamText_AssistantToolUseBash(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make test"}}]},"uuid":"jkl-012"}`)
	got := extractStreamText(line)
	assert.Equal(t, "--- Tool: Bash('make test') ---\n", got)
}

func TestExtractStreamText_ResultEvent(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"Done.\nCLOCHE_RESULT:success"}`)
	got := extractStreamText(line)
	assert.Equal(t, "Done.\nCLOCHE_RESULT:success", got)
}

func TestExtractStreamText_SystemEvent(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","cwd":"/workspace"}`)
	got := extractStreamText(line)
	assert.Empty(t, got)
}

func TestExtractStreamText_RateLimitEvent(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`)
	got := extractStreamText(line)
	assert.Empty(t, got)
}

func TestExtractStreamText_InvalidJSON(t *testing.T) {
	got := extractStreamText([]byte(`not json`))
	assert.Empty(t, got)
}

func TestExtractStreamText_EmptyContent(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[]},"uuid":"empty"}`)
	got := extractStreamText(line)
	assert.Empty(t, got)
}

func TestToolInputSummary_LongValue(t *testing.T) {
	longCmd := ""
	for i := 0; i < 100; i++ {
		longCmd += "x"
	}
	input := `{"command":"` + longCmd + `"}`
	got := toolInputSummary([]byte(input))
	assert.Contains(t, got, "...")
	assert.LessOrEqual(t, len(got), 70)
}

// Opencode --format json emits a distinct event stream from Claude's
// stream-json. These tests use fixtures captured from a live `opencode run
// --format json --model digitalocean/kimi-k2.6` invocation in the container.

func TestExtractStreamText_OpencodeTextEvent(t *testing.T) {
	line := []byte(`{"type":"text","timestamp":1779036439520,"sessionID":"ses_1","part":{"id":"prt_1","messageID":"msg_1","sessionID":"ses_1","type":"text","text":"Hello world"}}`)
	got := extractStreamText(line)
	assert.Equal(t, "Hello world", got)
}

func TestExtractStreamText_OpencodeTextEventEmpty(t *testing.T) {
	line := []byte(`{"type":"text","part":{"text":""}}`)
	got := extractStreamText(line)
	assert.Empty(t, got)
}

func TestExtractStreamText_OpencodeToolUse(t *testing.T) {
	line := []byte(`{"type":"tool_use","timestamp":1779036439520,"sessionID":"ses_1","part":{"type":"tool","tool":"write","callID":"functions.write:0","state":{"status":"completed","input":{"content":"HELLO","filePath":"/tmp/h.txt"},"output":"Wrote file successfully."}}}`)
	got := extractStreamText(line)
	assert.Equal(t, "--- Tool: write('/tmp/h.txt') ---\n", got)
}

func TestExtractStreamText_OpencodeToolUseBash(t *testing.T) {
	line := []byte(`{"type":"tool_use","part":{"tool":"bash","state":{"input":{"command":"go test ./..."}}}}`)
	got := extractStreamText(line)
	assert.Equal(t, "--- Tool: bash('go test ./...') ---\n", got)
}

func TestExtractStreamText_OpencodeStepStartIgnored(t *testing.T) {
	line := []byte(`{"type":"step_start","part":{}}`)
	got := extractStreamText(line)
	assert.Empty(t, got)
}

func TestExtractResultUsage_OpencodeStepFinish(t *testing.T) {
	line := []byte(`{"type":"step_finish","timestamp":1779036439520,"sessionID":"ses_1","part":{"id":"prt_1","reason":"tool-calls","messageID":"msg_1","sessionID":"ses_1","type":"step-finish","tokens":{"total":6687,"input":6620,"output":67,"reasoning":0,"cache":{"write":0,"read":0}},"cost":0.006557}}`)
	got := extractResultUsage(line)
	require.NotNil(t, got)
	assert.Equal(t, int64(6620), got.InputTokens)
	assert.Equal(t, int64(67), got.OutputTokens)
}

func TestExtractResultUsage_OpencodeStepFinishNoTokens(t *testing.T) {
	line := []byte(`{"type":"step_finish","part":{"reason":"stop"}}`)
	got := extractResultUsage(line)
	assert.Nil(t, got)
}

func TestExtractStreamText_TextWithoutTrailingNewline(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"no newline"}]},"uuid":"x"}`)
	got := extractStreamText(line)
	assert.Equal(t, "no newline\n", got, "should append newline to text without one")
}

func TestExtractResultUsage_WithUsage(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"Done.\nCLOCHE_RESULT:success","usage":{"input_tokens":12345,"output_tokens":6789}}`)
	got := extractResultUsage(line)
	require.NotNil(t, got)
	assert.Equal(t, int64(12345), got.InputTokens)
	assert.Equal(t, int64(6789), got.OutputTokens)
	// AgentName is not set by extractResultUsage itself; the caller (tryCommand) sets it.
	assert.Empty(t, got.AgentName)
}

func TestExtractResultUsage_NoUsageField(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","result":"Done.\nCLOCHE_RESULT:success"}`)
	got := extractResultUsage(line)
	assert.Nil(t, got)
}

func TestExtractResultUsage_NotResultEvent(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[]},"usage":{"input_tokens":100,"output_tokens":50}}`)
	got := extractResultUsage(line)
	assert.Nil(t, got)
}

func TestExtractResultUsage_InvalidJSON(t *testing.T) {
	got := extractResultUsage([]byte(`not json`))
	assert.Nil(t, got)
}

func TestScanOutputForUsage_FindsUsageInOutput(t *testing.T) {
	output := []byte(`{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":{"content":[]}}` + "\n" +
		`{"type":"result","subtype":"success","result":"CLOCHE_RESULT:success","usage":{"input_tokens":100,"output_tokens":50}}` + "\n")
	got := scanOutputForUsage(output)
	require.NotNil(t, got)
	assert.Equal(t, int64(100), got.InputTokens)
	assert.Equal(t, int64(50), got.OutputTokens)
}

func TestScanOutputForUsage_NoUsageInOutput(t *testing.T) {
	output := []byte(`{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"result","subtype":"success","result":"CLOCHE_RESULT:success"}` + "\n")
	got := scanOutputForUsage(output)
	assert.Nil(t, got)
}

func TestScanOutputForUsage_EmptyOutput(t *testing.T) {
	got := scanOutputForUsage([]byte{})
	assert.Nil(t, got)
}

func TestRunUsageCommand_Success(t *testing.T) {
	ctx := context.Background()
	got := runUsageCommand(ctx, `echo '{"input_tokens":100,"output_tokens":50}'`, t.TempDir())
	require.NotNil(t, got)
	assert.Equal(t, int64(100), got.InputTokens)
	assert.Equal(t, int64(50), got.OutputTokens)
}

func TestRunUsageCommand_CommandFails(t *testing.T) {
	ctx := context.Background()
	got := runUsageCommand(ctx, "exit 1", t.TempDir())
	assert.Nil(t, got)
}

func TestRunUsageCommand_BadJSON(t *testing.T) {
	ctx := context.Background()
	got := runUsageCommand(ctx, "echo 'not json'", t.TempDir())
	assert.Nil(t, got)
}

func TestRunUsageCommand_CommandNotFound(t *testing.T) {
	ctx := context.Background()
	got := runUsageCommand(ctx, "nonexistent-command-xyz", t.TempDir())
	assert.Nil(t, got)
}
