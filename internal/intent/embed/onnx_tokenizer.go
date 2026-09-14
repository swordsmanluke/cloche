package embed

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// bertTokenizer implements the BertTokenizer scheme (BasicTokenizer +
// WordPiece) used by the all-MiniLM-L6-v2 export: lowercase, accent
// stripping, punctuation/CJK splitting, then greedy-longest-match
// subword matching against a fixed vocabulary. It has no cgo dependency —
// only the ONNX Runtime session in onnx.go needs the build tag — so it is
// unit-testable without a working onnxruntime install.
//
// Reference: https://github.com/google-research/bert/blob/master/tokenization.py
type bertTokenizer struct {
	vocab map[string]int64

	clsID int64
	sepID int64
	padID int64
	unkID int64
}

const (
	clsToken = "[CLS]"
	sepToken = "[SEP]"
	padToken = "[PAD]"
	unkToken = "[UNK]"

	// maxInputCharsPerWord matches BERT's WordpieceTokenizer default: a
	// "word" (post basic-tokenization) longer than this is emitted as a
	// single [UNK] rather than attempted subword-by-subword.
	maxInputCharsPerWord = 100
)

// newBertTokenizer loads a WordPiece vocabulary from a vocab.txt file: one
// token per line, line number is the token's ID.
func newBertTokenizer(vocabPath string) (*bertTokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("intent: opening vocab file: %w", err)
	}
	defer f.Close()

	vocab := make(map[string]int64)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var id int64
	for scanner.Scan() {
		tok := scanner.Text()
		if tok == "" {
			continue
		}
		vocab[tok] = id
		id++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("intent: reading vocab file: %w", err)
	}

	t := &bertTokenizer{vocab: vocab}
	var ok bool
	if t.clsID, ok = vocab[clsToken]; !ok {
		return nil, fmt.Errorf("intent: vocab missing %s", clsToken)
	}
	if t.sepID, ok = vocab[sepToken]; !ok {
		return nil, fmt.Errorf("intent: vocab missing %s", sepToken)
	}
	if t.padID, ok = vocab[padToken]; !ok {
		return nil, fmt.Errorf("intent: vocab missing %s", padToken)
	}
	if t.unkID, ok = vocab[unkToken]; !ok {
		return nil, fmt.Errorf("intent: vocab missing %s", unkToken)
	}
	return t, nil
}

// encodeBatch tokenizes texts and pads every sequence to the longest one in
// the batch (capped at maxLen, including the [CLS]/[SEP] special tokens).
// It returns row-major [len(texts)][seqLen] slices for the three tensors
// the MiniLM ONNX export expects, plus the seqLen actually used.
func (t *bertTokenizer) encodeBatch(texts []string, maxLen int) (inputIDs, attentionMask, tokenTypeIDs [][]int64, seqLen int) {
	perText := make([][]int64, len(texts))
	for i, text := range texts {
		ids := t.encode(text, maxLen)
		perText[i] = ids
		if len(ids) > seqLen {
			seqLen = len(ids)
		}
	}

	inputIDs = make([][]int64, len(texts))
	attentionMask = make([][]int64, len(texts))
	tokenTypeIDs = make([][]int64, len(texts))
	for i, ids := range perText {
		row := make([]int64, seqLen)
		mask := make([]int64, seqLen)
		copy(row, ids)
		for j := range ids {
			mask[j] = 1
		}
		for j := len(ids); j < seqLen; j++ {
			row[j] = t.padID
		}
		inputIDs[i] = row
		attentionMask[i] = mask
		tokenTypeIDs[i] = make([]int64, seqLen) // all zero: single-segment input
	}
	return inputIDs, attentionMask, tokenTypeIDs, seqLen
}

// encode tokenizes a single text into [CLS] ... [SEP], truncated so the
// total (including both special tokens) never exceeds maxLen.
func (t *bertTokenizer) encode(text string, maxLen int) []int64 {
	budget := maxLen - 2
	if budget < 0 {
		budget = 0
	}

	ids := make([]int64, 0, budget+2)
	ids = append(ids, t.clsID)
	for _, word := range basicTokenize(text) {
		for _, piece := range t.wordpiece(word) {
			if len(ids) >= budget+1 { // +1 for the CLS already appended
				break
			}
			ids = append(ids, piece)
		}
	}
	ids = append(ids, t.sepID)
	return ids
}

// wordpiece greedily matches the longest known prefix of word (continuation
// pieces after the first are prefixed "##"), falling back to a single
// [UNK] when no split covers the whole word or the word is unreasonably
// long.
func (t *bertTokenizer) wordpiece(word string) []int64 {
	runes := []rune(word)
	if len(runes) > maxInputCharsPerWord {
		return []int64{t.unkID}
	}

	var out []int64
	start := 0
	for start < len(runes) {
		end := len(runes)
		var matchedID int64
		matched := false
		for start < end {
			substr := string(runes[start:end])
			if start > 0 {
				substr = "##" + substr
			}
			if id, ok := t.vocab[substr]; ok {
				matchedID = id
				matched = true
				break
			}
			end--
		}
		if !matched {
			return []int64{t.unkID}
		}
		out = append(out, matchedID)
		start = end
	}
	return out
}

// basicTokenize applies BERT's BasicTokenizer: clean/whitespace-normalize,
// pad CJK characters with spaces so they split into individual tokens,
// lowercase + strip accents (do_lower_case's implied strip_accents), split
// off punctuation as standalone tokens, then split on whitespace.
func basicTokenize(text string) []string {
	var cleaned strings.Builder
	for _, r := range text {
		if r == 0 || r == 0xFFFD || isControl(r) {
			continue
		}
		if isWhitespace(r) {
			cleaned.WriteRune(' ')
			continue
		}
		if isCJK(r) {
			cleaned.WriteRune(' ')
			cleaned.WriteRune(r)
			cleaned.WriteRune(' ')
			continue
		}
		cleaned.WriteRune(r)
	}

	var out []string
	for _, word := range strings.Fields(cleaned.String()) {
		word = stripAccents(strings.ToLower(word))
		out = append(out, splitOnPunctuation(word)...)
	}
	return out
}

// stripAccents NFD-decomposes s and drops combining marks (Unicode
// category Mn), matching BasicTokenizer._run_strip_accents.
func stripAccents(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitOnPunctuation breaks word into pieces at punctuation boundaries,
// with each punctuation rune becoming its own single-rune piece.
func splitOnPunctuation(word string) []string {
	runes := []rune(word)
	var out []string
	var cur strings.Builder
	for _, r := range runes {
		if isPunctuation(r) {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			out = append(out, string(r))
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// isControl matches BasicTokenizer._is_control: \t, \n, \r are treated as
// whitespace (handled separately), everything else in a Unicode "C*"
// category is a control character to drop.
func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.IsControl(r)
}

// isWhitespace matches BasicTokenizer._is_whitespace.
func isWhitespace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// isPunctuation matches BasicTokenizer._is_punctuation: the ASCII ranges
// BERT special-cases (which Unicode classifies as symbols, not
// punctuation, e.g. '$' or '^') plus anything in a Unicode "P*" category.
func isPunctuation(r rune) bool {
	switch {
	case r >= 33 && r <= 47, r >= 58 && r <= 64, r >= 91 && r <= 96, r >= 123 && r <= 126:
		return true
	}
	return unicode.IsPunct(r)
}

// isCJK matches BasicTokenizer._is_chinese_char's code point ranges.
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x20000 && r <= 0x2A6DF,
		r >= 0x2A700 && r <= 0x2B73F,
		r >= 0x2B740 && r <= 0x2B81F,
		r >= 0x2B820 && r <= 0x2CEAF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0x2F800 && r <= 0x2FA1F:
		return true
	}
	return false
}
