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

// Agents deep into a long context don't reliably honor "final line, by
// itself": they wrap the marker in backticks or bold, add prose after it, or
// keep talking on later lines. None of that may hide the marker.
func TestExtractNoncedResult_MarkerNotOnItsOwnLine(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   string
		clean  string
	}{
		{"backticked", "Done.\n`CLOCHE_RESULT:n0nce1:success`\n", "success", "Done.\n``\n"},
		{"bold", "**CLOCHE_RESULT:n0nce1:success**\n", "success", "****\n"},
		{"trailing prose", "CLOCHE_RESULT:n0nce1:success — all tests pass.\n", "success", " — all tests pass.\n"},
		{"trailing period", "CLOCHE_RESULT:n0nce1:needs_research.\n", "needs_research", ".\n"},
		{"leading prose", "Final answer: CLOCHE_RESULT:n0nce1:fail\n", "fail", "Final answer: \n"},
		{"output after marker", "CLOCHE_RESULT:n0nce1:success\n\nLet me know if you need anything else.\n", "success", "\nLet me know if you need anything else.\n"},
		{"hyphenated name", "CLOCHE_RESULT:n0nce1:needs-review\n", "needs-review", ""},
		{"trailing dash is punctuation", "CLOCHE_RESULT:n0nce1:success- done\n", "success", "- done\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, clean, found := protocol.ExtractNoncedResult([]byte(tc.output), "n0nce1")
			assert.True(t, found)
			assert.Equal(t, tc.want, result)
			assert.Equal(t, tc.clean, string(clean))
		})
	}
}

func TestExtractNoncedResult_LastWinsAcrossMixedPlacement(t *testing.T) {
	output := []byte("CLOCHE_RESULT:n0nce1:fail\nActually the fix worked: `CLOCHE_RESULT:n0nce1:success`\n")
	result, _, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.True(t, found)
	assert.Equal(t, "success", result)
}

func TestExtractNoncedResult_PrefixWithoutNameIsNotAMarker(t *testing.T) {
	output := []byte("print CLOCHE_RESULT:n0nce1: then the result name\n")
	result, clean, found := protocol.ExtractNoncedResult(output, "n0nce1")
	assert.False(t, found)
	assert.Empty(t, result)
	assert.Equal(t, string(output), string(clean))
}

// A bare-prefix scan (no nonce known, e.g. stripping markers from a previous
// step's output) must excise a nonced marker whole, not leave ":success"
// behind.
func TestExtractResult_ExcisesNoncedMarkerWhole(t *testing.T) {
	output := []byte("Wrote 4 candidates\nCLOCHE_RESULT:abc123:success\n")
	_, clean, found := protocol.ExtractResult(output)
	assert.True(t, found)
	assert.Equal(t, "Wrote 4 candidates\n", string(clean))
}
