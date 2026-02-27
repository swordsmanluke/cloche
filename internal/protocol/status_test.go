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
