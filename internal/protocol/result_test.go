package protocol_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestExtractResult_Found(t *testing.T) {
	output := []byte("some output\nCLOCHE_RESULT:needs_research\nmore output\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "needs_research", result)
	assert.NotContains(t, string(clean), "CLOCHE_RESULT")
	assert.Contains(t, string(clean), "some output")
	assert.Contains(t, string(clean), "more output")
}

func TestExtractResult_LastWins(t *testing.T) {
	output := []byte("CLOCHE_RESULT:first\nstuff\nCLOCHE_RESULT:second\n")
	result, _, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "second", result)
}

func TestExtractResult_NotFound(t *testing.T) {
	output := []byte("just normal output\nexit 0\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.False(t, found)
	assert.Empty(t, result)
	assert.Equal(t, output, clean)
}

func TestExtractResult_EmptyOutput(t *testing.T) {
	result, clean, found := protocol.ExtractResult([]byte{})
	assert.False(t, found)
	assert.Empty(t, result)
	assert.Empty(t, clean)
}

func TestExtractResult_MarkerOnly(t *testing.T) {
	output := []byte("CLOCHE_RESULT:success\n")
	result, clean, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "success", result)
	assert.Empty(t, string(clean))
}
