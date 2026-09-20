package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCondensePrevOutput_StreamJSONKeepsOnlyFinalResult(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"system","subtype":"init","tools":["Bash","Read"]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"reading sources"},{"type":"tool_use","name":"Read","input":{"file_path":"/big/file"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"HUGE TOOL RESULT"}]}}`,
		`{"type":"result","subtype":"success","result":"first iteration summary\nCLOCHE_RESULT:abc123:success"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"second pass"}]}}`,
		`{"type":"result","subtype":"success","result":"Wrote 4 candidates to candidates.json\nCLOCHE_RESULT:abc123:success\n"}`,
		``,
	}, "\n")

	assert.Equal(t, "Wrote 4 candidates to candidates.json", CondensePrevOutput([]byte(log)))
}

func TestCondensePrevOutput_NoResultEventFallsBackToText(t *testing.T) {
	log := strings.Join([]string{
		`{"type":"step_start","part":{}}`,
		`{"type":"text","part":{"text":"all "}}`,
		`{"type":"text","part":{"text":"done"}}`,
	}, "\n")

	assert.Equal(t, "all done", CondensePrevOutput([]byte(log)))
}

func TestCondensePrevOutput_PlainLogPassesThrough(t *testing.T) {
	log := "created 1, superseded 0, merged 0, dropped 0\n"
	assert.Equal(t, log, CondensePrevOutput([]byte(log)))

	// JSON that isn't a stream event (no "type") is still plain output.
	jsonLog := `{"actions":[]}` + "\n"
	assert.Equal(t, jsonLog, CondensePrevOutput([]byte(jsonLog)))
}
