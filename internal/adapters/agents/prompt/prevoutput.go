package prompt

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/swordsmanluke/cloche/internal/protocol"
)

// CondensePrevOutput reduces a predecessor step's output log to the text worth
// handing to a later agent step's prompt. Agent step logs hold the raw
// stream-json transcript (every tool call, tool result, and usage event),
// which is what the console renders but is useless — and can run to megabytes
// — as prompt context: a transcript that size crowds out the step's own
// instructions and the agent stops following them.
//
// For a stream-json log the result is the final "result" event's text (the
// last one, since the log accumulates across loop iterations); producers with
// no result event (opencode) fall back to their concatenated text output.
// Logs with no stream-json events at all (script steps) pass through
// unchanged. CLOCHE_RESULT marker lines are dropped from extracted text: the
// raw-line strip applied when the log was written can't see a marker embedded
// in a JSON string.
func CondensePrevOutput(log []byte) string {
	var final string
	var text strings.Builder
	sawEvent := false
	for _, line := range bytes.Split(log, []byte("\n")) {
		if !isStreamEvent(line) {
			continue
		}
		sawEvent = true
		if r := extractResultText(line); r != "" {
			final = r
			continue
		}
		text.WriteString(extractStreamText(line))
	}
	if !sawEvent {
		return string(log)
	}
	if final == "" {
		final = text.String()
	}
	return stripResultMarkers(final)
}

// isStreamEvent reports whether line is a streaming-JSON event object.
func isStreamEvent(line []byte) bool {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' || !bytes.Contains(line, []byte(`"type"`)) {
		return false
	}
	var envelope struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &envelope) == nil && envelope.Type != ""
}

func stripResultMarkers(s string) string {
	_, clean, _ := protocol.ExtractResult([]byte(s))
	return strings.TrimSpace(string(clean))
}
