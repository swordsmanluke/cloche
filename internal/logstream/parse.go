package logstream

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ParseClaudeLine parses a single Claude Code stream-json line and returns
// the human-readable text it represents. If the line is a protocol event
// with no user-visible content, ok is false. Non-JSON input is returned as-is.
func ParseClaudeLine(line []byte) (text string, ok bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return "", false
	}
	if line[0] != '{' {
		return string(line), true
	}

	var base struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &base) != nil {
		return string(line), true
	}

	switch base.Type {
	case "assistant":
		t := extractAssistantText(line)
		return t, t != ""
	case "result":
		t := extractResultText(line)
		return t, t != ""
	case "content_block_delta":
		t := extractDeltaText(line)
		return t, t != ""
	case "content_block_start":
		t := extractBlockStart(line)
		return t, t != ""
	default:
		// Skip system, user (tool results), and other protocol events
		return "", false
	}
}

// ParseClaudeStream converts raw Claude Code stream-json output into
// human-readable text. It extracts assistant text, tool call summaries,
// and error messages, discarding protocol-level events.
//
// If the input does not appear to be stream-json (no valid JSON lines
// detected), it is returned unchanged.
func ParseClaudeStream(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\n"))
	var out strings.Builder
	jsonLines := 0

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if line[0] != '{' {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}

		var base struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &base) != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		jsonLines++

		if text, ok := ParseClaudeLine(line); ok {
			out.WriteString(text)
		}
	}

	if jsonLines == 0 {
		return raw
	}

	return []byte(out.String())
}

// extractAssistantText handles Claude Code "assistant" events.
// Extracts text content from message.content blocks.
func extractAssistantText(line []byte) string {
	var event struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"` // for tool_use blocks
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return ""
	}

	var parts []string
	for _, block := range event.Message.Content {
		switch block.Type {
		case "text":
			if t := strings.TrimSpace(block.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			parts = append(parts, "\n--- Tool: "+block.Name+" ---")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n") + "\n"
}

// extractDeltaText handles content_block_delta events (streaming API format).
func extractDeltaText(line []byte) string {
	if bytes.Contains(line, []byte(`"text_delta"`)) {
		var event struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(line, &event) == nil && event.Delta.Type == "text_delta" {
			return event.Delta.Text
		}
	}
	return ""
}

// extractBlockStart handles content_block_start events (streaming API format).
func extractBlockStart(line []byte) string {
	if !bytes.Contains(line, []byte(`"tool_use"`)) {
		return ""
	}
	var event struct {
		ContentBlock struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if json.Unmarshal(line, &event) == nil && event.ContentBlock.Type == "tool_use" {
		return "\n--- Tool: " + event.ContentBlock.Name + " ---\n"
	}
	return ""
}

// extractResultText handles result events (final output).
func extractResultText(line []byte) string {
	var event struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}
	if json.Unmarshal(line, &event) == nil && event.Type == "result" {
		return event.Result
	}
	return ""
}
