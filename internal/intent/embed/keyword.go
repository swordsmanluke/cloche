package embed

import (
	"context"
	"hash/fnv"
	"strings"
)

// keywordDimensions is the fixed size of the hashed bag-of-words vectors
// produced by KeywordEmbedder. Large enough relative to the hundreds-of-
// requirements corpus size the design expects that hash collisions rarely
// distort similarity.
const keywordDimensions = 1024

func init() {
	Register(keywordName, func(context.Context) bool { return true }, func(context.Context) (Embedder, error) {
		return NewKeywordEmbedder(), nil
	})
}

// KeywordEmbedder is the always-available degraded fallback: it scores
// similarity by token overlap rather than a trained model. The Embedder
// interface only exposes vectors compared by cosine similarity (the same
// code path the index uses for every adapter), so overlap is computed via
// the standard hashing-trick vectorizer: each token votes, with a sign
// derived from a second hash to keep collisions from biasing the sum, into
// a fixed-size vector that is then unit-normalized. Cosine similarity
// between two such vectors approximates token overlap between the two
// texts.
type KeywordEmbedder struct{}

// NewKeywordEmbedder returns the token-overlap fallback embedder.
func NewKeywordEmbedder() *KeywordEmbedder {
	return &KeywordEmbedder{}
}

func (k *KeywordEmbedder) ModelID() string { return "keyword:v1" }

func (k *KeywordEmbedder) Dimensions() int { return keywordDimensions }

func (k *KeywordEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		vecs[i] = hashEmbed(t)
	}
	return vecs, nil
}

func hashEmbed(text string) []float32 {
	v := make([]float32, keywordDimensions)
	for _, tok := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum32()

		idx := sum % keywordDimensions
		sign := float32(1)
		if sum&0x10000 != 0 {
			sign = -1
		}
		v[idx] += sign
	}
	normalize(v)
	return v
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(
		"a an the of to in on for from with and or is are was be been it this that " +
			"as at by not no never must should can into over after before new my i we you",
	) {
		m[w] = true
	}
	return m
}()

// tokenize lowercases and splits s into alphanumeric tokens, dropping
// single-character tokens and stopwords.
func tokenize(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if w := b.String(); len(w) > 1 && !stopwords[w] {
			tokens = append(tokens, w)
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}
