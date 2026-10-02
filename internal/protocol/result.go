package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

const ResultPrefix = "CLOCHE_RESULT:"

// ExtractResult scans output for the last CLOCHE_RESULT:<name> marker.
// Returns the result name, the output with all markers removed, and whether
// a marker was found. See extractMarkers for what counts as a marker.
func ExtractResult(output []byte) (result string, cleanOutput []byte, found bool) {
	return extractMarkers(output, ResultPrefix)
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

// ExtractNoncedResult scans output for the last marker of the form
// CLOCHE_RESULT:<nonce>:<name>, where nonce must match exactly. This is the
// out-of-band framing that keeps free-form agent transcripts from poisoning
// their own classification: static text elsewhere in the codebase (grep
// output, test fixtures, docs quoting the bare "CLOCHE_RESULT:success"
// protocol) can never carry this run's random nonce, so it is left alone as
// ordinary text rather than mistaken for the genuine terminal marker.
func ExtractNoncedResult(output []byte, nonce string) (result string, cleanOutput []byte, found bool) {
	return extractMarkers(output, noncePrefix(nonce))
}

// extractMarkers finds every occurrence of prefix followed by a result name
// in output. The marker need not be the last line, start its line, or sit on
// a line by itself: agents deep into a long context regularly wrap it in
// backticks, tack prose on after it, or keep talking on later lines, and the
// prompt's instructions are the only thing asking them not to. The result
// name is the run of identifier characters ([A-Za-z0-9_-], the DSL's result
// name syntax) immediately after the prefix, so "`CLOCHE_RESULT:n:success`."
// and "CLOCHE_RESULT:n:success — all tests pass" both yield "success". A
// prefix followed by no name is not a marker.
//
// The last marker in the output wins. cleanOutput is output with every
// marker excised; a line left with nothing but whitespace is dropped.
func extractMarkers(output []byte, prefix string) (result string, cleanOutput []byte, found bool) {
	var clean [][]byte
	for _, line := range bytes.Split(output, []byte("\n")) {
		stripped, name, ok := excise(line, prefix)
		if ok {
			result = name
			found = true
			if len(bytes.TrimSpace(stripped)) == 0 {
				continue
			}
		}
		clean = append(clean, stripped)
	}
	joined := bytes.Join(clean, []byte("\n"))
	joined = bytes.TrimRight(joined, "\n")
	if len(joined) > 0 {
		joined = append(joined, '\n')
	}
	return result, joined, found
}

// excise removes every prefix+name marker from line, returning the stripped
// line, the last name found, and whether any marker was present.
func excise(line []byte, prefix string) (stripped []byte, name string, ok bool) {
	p := []byte(prefix)
	rest := line
	var out []byte
	for {
		i := bytes.Index(rest, p)
		if i < 0 {
			break
		}
		nameStart := i + len(p)
		nameEnd := nameStart
		for nameEnd < len(rest) && isResultNameChar(rest[nameEnd]) {
			nameEnd++
		}
		candidate := strings.TrimRight(string(rest[nameStart:nameEnd]), "-")
		if candidate == "" {
			out = append(out, rest[:nameStart]...)
			rest = rest[nameStart:]
			continue
		}
		end := nameStart + len(candidate)
		// A bare-prefix scan over a nonced marker ("CLOCHE_RESULT:<nonce>:
		// <name>") sees <nonce> as the name; swallow the ":<name>" tail(s)
		// too so the whole marker is excised rather than leaving ":success"
		// behind in the cleaned output.
		for end+1 < len(rest) && rest[end] == ':' && isResultNameChar(rest[end+1]) {
			end++
			for end < len(rest) && isResultNameChar(rest[end]) {
				end++
			}
		}
		name, ok = candidate, true
		out = append(out, rest[:i]...)
		rest = rest[end:]
	}
	if !ok {
		return line, "", false
	}
	return append(out, rest...), name, true
}

func isResultNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}
