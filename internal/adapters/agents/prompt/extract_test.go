package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestExtractStreamText_TextWithoutTrailingNewline(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"no newline"}]},"uuid":"x"}`)
	got := extractStreamText(line)
	assert.Equal(t, "no newline\n", got, "should append newline to text without one")
}
