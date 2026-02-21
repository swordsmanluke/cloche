package protocol

import (
	"bytes"
	"strings"
)

const ResultPrefix = "CLOCHE_RESULT:"

// ExtractResult scans output for the last CLOCHE_RESULT:<name> line.
// Returns the result name, the output with all marker lines removed, and
// whether a marker was found.
func ExtractResult(output []byte) (result string, cleanOutput []byte, found bool) {
	var clean [][]byte
	for _, line := range bytes.Split(output, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, ResultPrefix) {
			result = trimmed[len(ResultPrefix):]
			found = true
		} else {
			clean = append(clean, line)
		}
	}
	// Rejoin and trim trailing empty line from split
	joined := bytes.Join(clean, []byte("\n"))
	joined = bytes.TrimRight(joined, "\n")
	if len(joined) > 0 {
		joined = append(joined, '\n')
	}
	return result, joined, found
}
