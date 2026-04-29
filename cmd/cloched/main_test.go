package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterNestedProjects(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "no nesting",
			input: []string{"/a/proj1", "/a/proj2", "/b/proj3"},
			want:  []string{"/a/proj1", "/a/proj2", "/b/proj3"},
		},
		{
			name:  "child filtered",
			input: []string{"/root/project", "/root/project/vendor/sub"},
			want:  []string{"/root/project"},
		},
		{
			name:  "parent kept when listed after child",
			input: []string{"/root/project/vendor/sub", "/root/project"},
			want:  []string{"/root/project"},
		},
		{
			name:  "multiple nested",
			input: []string{"/root", "/root/a", "/root/b", "/other"},
			want:  []string{"/other", "/root"},
		},
		{
			name:  "prefix boundary respected",
			input: []string{"/root/proj", "/root/proj2"},
			want:  []string{"/root/proj", "/root/proj2"},
		},
		{
			name:  "empty input",
			input: nil,
			want:  nil,
		},
		{
			name:  "single entry",
			input: []string{"/only"},
			want:  []string{"/only"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterNestedProjects(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProjectIsSubpath(t *testing.T) {
	tests := []struct {
		parent string
		child  string
		want   bool
	}{
		{"/root/proj", "/root/proj/sub", true},
		{"/root/proj", "/root/proj/a/b/c", true},
		{"/root/proj", "/root/proj", false},
		{"/root/proj", "/root/proj2", false},
		{"/root/proj", "/root/other", false},
		{"/root", "/root/proj", true},
		{"/root/proj/sub", "/root/proj", false},
	}

	for _, tc := range tests {
		got := projectIsSubpath(tc.parent, tc.child)
		assert.Equal(t, tc.want, got, "projectIsSubpath(%q, %q)", tc.parent, tc.child)
	}
}
