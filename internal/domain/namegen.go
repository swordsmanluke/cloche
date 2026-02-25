package domain

import (
	"fmt"
	"math/rand"
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
// <workflow>-<adjective>-<noun>.
func GenerateRunID(workflowName string) string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s-%s", workflowName, adj, noun)
}
