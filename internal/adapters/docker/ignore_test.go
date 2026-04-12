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

# Runtime state
.cloche/runs/
.cloche/logs/

# Git internals
.git/
.gitworktrees/

# Build artifacts
bin/
obj/

# Temp files
*.tmp

# Beads sockets
.beads/*.sock
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".clocheignore"), []byte(content), 0644))

	patterns, err := parseClocheignore(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, patterns)

	// ".cloche/runs/" → anchored (contains /), dirOnly
	p := patterns[0]
	assert.Equal(t, ".cloche/runs", p.pattern)
	assert.True(t, p.dirOnly)
	assert.True(t, p.anchored)
	assert.False(t, p.negated)

	// "*.tmp" → not dirOnly, matchBase (no slash)
	var tmpPattern ignorePattern
	for _, p := range patterns {
		if p.pattern == "*.tmp" {
			tmpPattern = p
			break
		}
	}
	assert.Equal(t, "*.tmp", tmpPattern.pattern)
	assert.False(t, tmpPattern.dirOnly)
	assert.True(t, tmpPattern.matchBase)

	// ".beads/*.sock" → anchored (contains /), not dirOnly
	var sockPattern ignorePattern
	for _, p := range patterns {
		if p.pattern == ".beads/*.sock" {
			sockPattern = p
			break
		}
	}
	assert.True(t, sockPattern.anchored)
	assert.False(t, sockPattern.dirOnly)
}

func TestParseClocheignore_Missing(t *testing.T) {
	patterns, err := parseClocheignore(t.TempDir())
	assert.NoError(t, err)
	assert.Nil(t, patterns)
}

func TestIsIgnored(t *testing.T) {
	patterns := []ignorePattern{
		{pattern: ".cloche/runs", dirOnly: true, anchored: true},
		{pattern: ".cloche/logs", dirOnly: true, anchored: true},
		{pattern: ".git", dirOnly: true, matchBase: true},
		{pattern: "bin", dirOnly: true, matchBase: true},
		{pattern: "*.tmp", matchBase: true},
		{pattern: ".beads/*.sock", anchored: true},
	}

	tests := []struct {
		path    string
		isDir   bool
		ignored bool
	}{
		// Runtime state directories should be ignored.
		{".cloche/runs", true, true},
		{".cloche/logs", true, true},

		// Git and build directories.
		{".git", true, true},
		{"bin", true, true},

		// dirOnly patterns should NOT match files.
		{".git", false, false},
		{"bin", false, false},

		// matchBase matches basename at any depth.
		{"subdir/bin", true, true},
		{"deep/nested/.git", true, true},

		// *.tmp matches files anywhere.
		{"foo.tmp", false, true},
		{"subdir/bar.tmp", false, true},

		// Anchored glob pattern.
		{".beads/foo.sock", false, true},
		{".beads/bar.sock", false, true},
		{".beads/notasock", false, false},

		// Cloche workflow files should NOT be ignored.
		{".cloche/develop.cloche", false, false},
		{".cloche/host.cloche", false, false},
		{".cloche/prompts/implement.md", false, false},

		// Source files should NOT be ignored.
		{"internal/domain/run.go", false, false},
		{"cmd/cloche/main.go", false, false},
		{"go.mod", false, false},
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
