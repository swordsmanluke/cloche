package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
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

// GenerateNonce returns a short random hex string suitable for framing a
// CLOCHE_RESULT marker (see ExtractNoncedResult) so it can't be forged by
// quoting static text. Falls back to a timestamp-derived value in the
// astronomically unlikely case crypto/rand is unavailable, rather than
// returning an error every caller would have to handle.
func GenerateNonce() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))[:12]
	}
	return hex.EncodeToString(b)
}

// noncePrefix returns the marker prefix that frames a result for the given
// per-step nonce: "CLOCHE_RESULT:<nonce>:".
func noncePrefix(nonce string) string {
	return ResultPrefix + nonce + ":"
}

// FormatNoncedMarker returns the marker line text (without trailing newline)
// an agent should print to report the given result name under the given
// nonce: "CLOCHE_RESULT:<nonce>:<name>".
func FormatNoncedMarker(nonce, name string) string {
	return noncePrefix(nonce) + name
}

// ExtractNoncedResult scans output for the last standalone line of the form
// CLOCHE_RESULT:<nonce>:<name>, where nonce must match exactly. This is the
// out-of-band framing that keeps free-form agent transcripts from poisoning
// their own classification: static text elsewhere in the codebase (grep
// output, test fixtures, docs quoting the bare "CLOCHE_RESULT:success"
// protocol) can never carry this run's random nonce, so it is left alone as
// ordinary text rather than mistaken for the genuine terminal marker.
func ExtractNoncedResult(output []byte, nonce string) (result string, cleanOutput []byte, found bool) {
	prefix := noncePrefix(nonce)
	var clean [][]byte
	for _, line := range bytes.Split(output, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, prefix) {
			result = trimmed[len(prefix):]
			found = true
		} else {
			clean = append(clean, line)
		}
	}
	joined := bytes.Join(clean, []byte("\n"))
	joined = bytes.TrimRight(joined, "\n")
	if len(joined) > 0 {
		joined = append(joined, '\n')
	}
	return result, joined, found
}
