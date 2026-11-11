package dra

import "testing"

// The DRA name is the domain; domains (intent/scan domainNameRe) require
// alphanumeric runs separated by single hyphens.
func TestValidNameRejectsLeadingTrailingDoubleHyphen(t *testing.T) {
	for _, n := range []string{"-", "-a", "a-", "a--b"} {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; not a valid domain/label name", n)
		}
	}
}
