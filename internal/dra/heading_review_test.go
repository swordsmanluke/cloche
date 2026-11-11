package dra

import "testing"

func TestFirstHeadingNotATXHeadings(t *testing.T) {
	cases := map[string]string{
		"#hashtag not a heading\n# Real\n":          "Real",
		"~~~\n```\n# inner\n~~~\n# Real\n":           "Real",
		"    # indented code\n# Real\n":              "Real",
		"####### seven hashes\n# Real\n":            "Real",
		"# Title ##\n":                               "Title",
	}
	for in, want := range cases {
		if got := FirstHeading(in); got != want {
			t.Errorf("FirstHeading(%q) = %q, want %q", in, got, want)
		}
	}
}
