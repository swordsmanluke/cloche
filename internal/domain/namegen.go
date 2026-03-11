package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand"
)

var adjectives = []string{
	"swift", "bright", "calm", "dark", "eager",
	"fair", "grand", "happy", "keen", "lush",
	"bold", "cool", "deft", "firm", "glad",
	"warm", "wild", "wise", "crisp", "fresh",
	"light", "quick", "sharp", "quiet", "vivid",
	"brave", "clear", "gentle", "proud", "steady",
}

var nouns = []string{
	"oak", "elm", "fox", "hawk", "lake",
	"pine", "reef", "sage", "vale", "wolf",
	"bear", "cove", "dale", "fern", "glen",
	"hare", "jade", "lark", "moss", "nest",
	"peak", "reed", "star", "tide", "vine",
	"birch", "brook", "crane", "drift", "flint",
}

// GenerateRunID produces a human-readable run ID in the format
// <workflow>-<adjective>-<noun>-<hex>.
func GenerateRunID(workflowName string) string {
	adj := adjectives[mathrand.Intn(len(adjectives))]
	noun := nouns[mathrand.Intn(len(nouns))]
	var b [2]byte
	rand.Read(b[:])
	return fmt.Sprintf("%s-%s-%s-%s", workflowName, adj, noun, hex.EncodeToString(b[:]))
}
