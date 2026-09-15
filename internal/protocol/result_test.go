package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swordsmanluke/cloche/internal/protocol"
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

func TestGenerateNonce_UniqueAndNonEmpty(t *testing.T) {
	a := protocol.GenerateNonce()
	b := protocol.GenerateNonce()
	assert.NotEmpty(t, a)
	assert.NotEmpty(t, b)
	assert.NotEqual(t, a, b)
}

func TestFormatNoncedMarker(t *testing.T) {
	assert.Equal(t, "CLOCHE_RESULT:abc123:success", protocol.FormatNoncedMarker("abc123", "success"))
}

// TestExtractNoncedResult_IgnoresQuotedMarkersWithoutNonce is the regression
// test for the vulnerability this framing closes: a transcript that merely
// quotes the bare "CLOCHE_RESULT:success" protocol string (grepping code,
// echoing a test fixture, discussing the marker itself) must not be mistaken
// for the genuine terminal marker, which only this run's nonce can produce.
func TestExtractNoncedResult_IgnoresQuotedMarkersWithoutNonce(t *testing.T) {
	output := []byte("grep found: CLOCHE_RESULT:success in fixtures_test.go\n" +
		"CLOCHE_RESULT:success\n" + // bare, no nonce — must be ignored
		"more output\n")
	result, clean, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.False(t, found)
	assert.Empty(t, result)
	// Nothing was stripped: none of these lines carried the nonce.
	assert.Equal(t, string(output), string(clean))
}

func TestExtractNoncedResult_HonorsMatchingNonce(t *testing.T) {
	output := []byte("some prose\nCLOCHE_RESULT:n0nce1:success\nmore prose\n")
	result, clean, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.True(t, found)
	assert.Equal(t, "success", result)
	assert.NotContains(t, string(clean), "CLOCHE_RESULT")
	assert.Contains(t, string(clean), "some prose")
	assert.Contains(t, string(clean), "more prose")
}

func TestExtractNoncedResult_WrongNonceNotHonored(t *testing.T) {
	output := []byte("CLOCHE_RESULT:other-nonce:success\n")
	result, _, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.False(t, found)
	assert.Empty(t, result)
}

func TestExtractNoncedResult_LastWins(t *testing.T) {
	output := []byte("CLOCHE_RESULT:n0nce1:first\nstuff\nCLOCHE_RESULT:n0nce1:second\n")
	result, _, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.True(t, found)
	assert.Equal(t, "second", result)
}
