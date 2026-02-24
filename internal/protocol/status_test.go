package protocol_test

import (
	"bytes"
	"testing"

	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusWriter_WritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewStatusWriter(&buf)

	w.StepStarted("code")
	w.StepCompleted("code", "success")
	w.RunCompleted("succeeded")

	msgs, err := protocol.ParseStatusStream(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	assert.Equal(t, protocol.MsgStepStarted, msgs[0].Type)
	assert.Equal(t, "code", msgs[0].StepName)

	assert.Equal(t, protocol.MsgStepCompleted, msgs[1].Type)
	assert.Equal(t, "success", msgs[1].Result)

	assert.Equal(t, protocol.MsgRunCompleted, msgs[2].Type)
	assert.Equal(t, "succeeded", msgs[2].Result)
}

func TestStatusMessagePromptCapture(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewStatusWriter(&buf)

	w.StepStartedWithPrompt("implement", "the assembled prompt")
	w.StepCompletedWithCapture("implement", "success", "agent output here", 2)

	msgs, err := protocol.ParseStatusStream(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Equal(t, protocol.MsgStepStarted, msgs[0].Type)
	assert.Equal(t, "the assembled prompt", msgs[0].PromptText)

	assert.Equal(t, protocol.MsgStepCompleted, msgs[1].Type)
	assert.Equal(t, "agent output here", msgs[1].AgentOutput)
	assert.Equal(t, 2, msgs[1].AttemptNumber)
}

func TestStatusWriter_LogMessage(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewStatusWriter(&buf)

	w.Log("code", "running tests...")

	msgs, err := protocol.ParseStatusStream(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	assert.Equal(t, protocol.MsgLog, msgs[0].Type)
	assert.Equal(t, "running tests...", msgs[0].Message)
}
