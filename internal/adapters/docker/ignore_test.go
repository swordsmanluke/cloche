package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClocheignore(t *testing.T) {
	dir := t.TempDir()
	content := `# Comment
shandalar_2015/
assets/
.godot/

# Git internals
.git/
.gitworktrees/

# Godot engine
godot
GodotSharp/

# Build artifacts
bin/
obj/

# Cloche run state
.cloche/*-*-*/
.cloche/run-*/
.cloche/attempt_count/

# Beads sockets
.beads/*.sock
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".clocheignore"), []byte(content), 0644))

	patterns, err := parseClocheignore(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, patterns)

	// Verify a few parsed patterns.
	// "shandalar_2015/" → dirOnly, matchBase
	p := patterns[0]
	assert.Equal(t, "shandalar_2015", p.pattern)
	assert.True(t, p.dirOnly)
	assert.True(t, p.matchBase)
	assert.False(t, p.negated)

	// "godot" → not dirOnly, matchBase
	var godotPattern ignorePattern
	for _, p := range patterns {
		if p.pattern == "godot" {
			godotPattern = p
			break
		}
	}
	assert.Equal(t, "godot", godotPattern.pattern)
	assert.False(t, godotPattern.dirOnly)
	assert.True(t, godotPattern.matchBase)

	// ".cloche/*-*-*/" → anchored (contains /), dirOnly
	var clochePattern ignorePattern
	for _, p := range patterns {
		if p.pattern == ".cloche/*-*-*" {
			clochePattern = p
			break
		}
	}
	assert.True(t, clochePattern.anchored)
	assert.True(t, clochePattern.dirOnly)
}

func TestParseClocheignore_Missing(t *testing.T) {
	patterns, err := parseClocheignore(t.TempDir())
	assert.NoError(t, err)
	assert.Nil(t, patterns)
}

func TestIsIgnored(t *testing.T) {
	patterns := []ignorePattern{
		{pattern: "shandalar_2015", dirOnly: true, matchBase: true},
		{pattern: "assets", dirOnly: true, matchBase: true},
		{pattern: ".godot", dirOnly: true, matchBase: true},
		{pattern: ".git", dirOnly: true, matchBase: true},
		{pattern: "godot", matchBase: true},
		{pattern: "GodotSharp", dirOnly: true, matchBase: true},
		{pattern: "bin", dirOnly: true, matchBase: true},
		{pattern: ".cloche/*-*-*", dirOnly: true, anchored: true},
		{pattern: ".beads/*.sock", anchored: true},
	}

	tests := []struct {
		path    string
		isDir   bool
		ignored bool
	}{
		// Directories that should be ignored.
		{"shandalar_2015", true, true},
		{"assets", true, true},
		{".godot", true, true},
		{".git", true, true},
		{"GodotSharp", true, true},
		{"bin", true, true},

		// "godot" as a file (no trailing / in pattern) should also match.
		{"godot", false, true},
		// "godot" as a directory should match too.
		{"godot", true, true},

		// dirOnly patterns should NOT match files.
		{"shandalar_2015", false, false},
		{"assets", false, false},
		{".git", false, false},

		// Nested paths: matchBase matches basename.
		{"subdir/bin", true, true},
		{"deep/nested/assets", true, true},

		// Anchored pattern with glob.
		{".cloche/develop-warm-fern-d0c4", true, true},
		{".cloche/run-abc123", true, false}, // doesn't match *-*-*

		// Glob in anchored pattern.
		{".beads/foo.sock", false, true},
		{".beads/bar.sock", false, true},
		{".beads/notasock", false, false},

		// Files that should NOT be ignored.
		{"src/main.go", false, false},
		{".cloche/develop.cloche", false, false},
		{"README.md", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isIgnored(patterns, tt.path, tt.isDir)
			assert.Equal(t, tt.ignored, got, "isIgnored(%q, isDir=%v)", tt.path, tt.isDir)
		})
	}
}

func TestIsIgnored_Negation(t *testing.T) {
	patterns := []ignorePattern{
		{pattern: "*.log", matchBase: true},
		{pattern: "important.log", matchBase: true, negated: true},
	}

	assert.True(t, isIgnored(patterns, "debug.log", false))
	assert.False(t, isIgnored(patterns, "important.log", false))
	assert.True(t, isIgnored(patterns, "subdir/other.log", false))
	assert.False(t, isIgnored(patterns, "subdir/important.log", false))
}

func TestIsIgnored_DoublestarPatterns(t *testing.T) {
	patterns := []ignorePattern{
		{pattern: "**/*.tmp", anchored: true},
		{pattern: "docs/**", anchored: true},
	}

	assert.True(t, isIgnored(patterns, "foo.tmp", false))
	assert.True(t, isIgnored(patterns, "a/b/c.tmp", false))
	assert.True(t, isIgnored(patterns, "docs/readme.md", false))
	assert.True(t, isIgnored(patterns, "docs/sub/file.txt", false))
	assert.False(t, isIgnored(patterns, "src/main.go", false))
}

func TestIsIgnored_Empty(t *testing.T) {
	assert.False(t, isIgnored(nil, "anything", false))
	assert.False(t, isIgnored(nil, "anything", true))
}
