package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version returns the current Cloche version string (e.g. "0.1.0").
// Lines beginning with '#' in the VERSION file are treated as comments and ignored.
func Version() string {
	for _, line := range strings.Split(rawVersion, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}
