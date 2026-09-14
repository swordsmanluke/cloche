package embed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureVocab writes a small WordPiece vocabulary (one token per line, ID
// = line number) to a temp file and returns its path. Large enough to
// exercise special tokens, whole-word matches, and "##"-continuation
// splitting, without needing the real ~30k-entry vocab.txt.
func fixtureVocab(t *testing.T) string {
	t.Helper()
	tokens := []string{
		"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]",
		"hello", "world", "play", "##ing", "cafe", "test", "run", "##s",
	}
	path := filepath.Join(t.TempDir(), "vocab.txt")
	var content string
	for _, tok := range tokens {
		content += tok + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestBertTokenizer_WholeWordMatch(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	ids := tok.encode("hello world", 10)
	// [CLS] hello world [SEP]
	require.Len(t, ids, 4)
	assert.Equal(t, tok.clsID, ids[0])
	assert.Equal(t, tok.vocab["hello"], ids[1])
	assert.Equal(t, tok.vocab["world"], ids[2])
	assert.Equal(t, tok.sepID, ids[3])
}

func TestBertTokenizer_WordpieceSplitsUnknownCompound(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	ids := tok.encode("playing", 10)
	// [CLS] play ##ing [SEP]
	require.Len(t, ids, 4)
	assert.Equal(t, tok.vocab["play"], ids[1])
	assert.Equal(t, tok.vocab["##ing"], ids[2])
}

func TestBertTokenizer_UnknownTokenFallsBackToUNK(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	ids := tok.encode("xyzzy", 10)
	require.Len(t, ids, 3) // [CLS] [UNK] [SEP]
	assert.Equal(t, tok.unkID, ids[1])
}

func TestBertTokenizer_LowercasesAndStripsAccents(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	ids := tok.encode("CAFÉ", 10)
	require.Len(t, ids, 3)
	assert.Equal(t, tok.vocab["cafe"], ids[1])
}

func TestBertTokenizer_TruncatesToMaxLen(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	ids := tok.encode("hello world test run", 4)
	require.Len(t, ids, 4)
	assert.Equal(t, tok.clsID, ids[0])
	assert.Equal(t, tok.sepID, ids[3])
}

func TestBertTokenizer_EncodeBatchPadsToLongestAndSetsAttentionMask(t *testing.T) {
	tok, err := newBertTokenizer(fixtureVocab(t))
	require.NoError(t, err)

	inputIDs, attentionMask, tokenTypeIDs, seqLen := tok.encodeBatch([]string{"hello", "hello world test"}, 32)

	require.Len(t, inputIDs, 2)
	assert.Equal(t, seqLen, len(inputIDs[0]))
	assert.Equal(t, seqLen, len(inputIDs[1]))

	// Row 0 ("hello" -> [CLS] hello [SEP], length 3) is padded out to
	// seqLen with [PAD] and a zeroed attention mask past position 2.
	for i := 3; i < seqLen; i++ {
		assert.Equal(t, tok.padID, inputIDs[0][i])
		assert.Equal(t, int64(0), attentionMask[0][i])
	}
	for i := 0; i < 3; i++ {
		assert.Equal(t, int64(1), attentionMask[0][i])
	}

	// token_type_ids is single-segment input: all zero.
	for _, row := range tokenTypeIDs {
		for _, v := range row {
			assert.Equal(t, int64(0), v)
		}
	}
}

func TestBertTokenizer_MissingSpecialTokenIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vocab.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello\nworld\n"), 0o644)) // no [CLS]/[SEP]/[PAD]/[UNK]

	_, err := newBertTokenizer(path)
	assert.Error(t, err)
}

func TestBasicTokenize_SplitsPunctuationAndCJK(t *testing.T) {
	assert.Equal(t, []string{"hello", ",", "world", "!"}, basicTokenize("Hello, World!"))
	assert.Equal(t, []string{"你", "好"}, basicTokenize("你好"))
}

func TestStripAccents(t *testing.T) {
	assert.Equal(t, "cafe", stripAccents("café"))
	assert.Equal(t, "naive", stripAccents("naïve"))
}

func TestIsPunctuation_IncludesASCIISymbolRangesBERTTreatsAsPunctuation(t *testing.T) {
	// '$' and '^' are Unicode "symbol" category, not "punctuation" —
	// BERT's tokenizer still special-cases them as punctuation via the
	// ASCII range check.
	assert.True(t, isPunctuation('$'))
	assert.True(t, isPunctuation('^'))
	assert.True(t, isPunctuation(','))
	assert.False(t, isPunctuation('a'))
	assert.False(t, isPunctuation('5'))
}
