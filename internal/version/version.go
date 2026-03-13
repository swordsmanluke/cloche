package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version returns the current Cloche version string (e.g. "0.1.0").
func Version() string {
	return strings.TrimSpace(rawVersion)
}
