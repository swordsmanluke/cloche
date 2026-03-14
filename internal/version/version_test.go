package version

import (
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string")
	}
}

func TestVersionNoTrailingNewline(t *testing.T) {
	v := Version()
	if v != rawVersion[:len(rawVersion)-1] && v == rawVersion {
		t.Fatal("Version() should strip trailing whitespace")
	}
}
