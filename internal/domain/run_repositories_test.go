package domain

import (
	"reflect"
	"testing"
)

func TestResolveRunRepositories(t *testing.T) {
	tests := []struct {
		name        string
		explicit    string
		projectRepo string
		touched     []string
		wfRepos     []string
		want        []string
	}{
		{
			name: "no signals is unattributed",
		},
		{
			name:     "rule a: explicit single repo",
			explicit: "backend",
			want:     []string{"backend"},
		},
		{
			name:        "rule b: project_dir matches a repo path",
			projectRepo: "cloche",
			want:        []string{"cloche"},
		},
		{
			name:    "rule c: touched repos",
			touched: []string{"frontend", "backend"},
			want:    []string{"frontend", "backend"},
		},
		{
			name:    "rule d: workflow declares multiple repos",
			wfRepos: []string{"backend", "frontend"},
			want:    []string{"backend", "frontend"},
		},
		{
			name:    "rule d: workflow declares a single repo is not re-added (already rule a's job)",
			wfRepos: []string{"backend"},
			want:    nil,
		},
		{
			name:     "rules union and dedupe across sources",
			explicit: "backend",
			touched:  []string{"backend", "frontend"},
			wfRepos:  []string{"backend", "frontend"},
			want:     []string{"backend", "frontend"},
		},
		{
			name:        "rule order: explicit, then project repo, then touched, then declared",
			explicit:    "a",
			projectRepo: "b",
			touched:     []string{"c"},
			wfRepos:     []string{"d", "e"},
			want:        []string{"a", "b", "c", "d", "e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveRunRepositories(tt.explicit, tt.projectRepo, tt.touched, tt.wfRepos)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveRunRepositories(%q, %q, %v, %v) = %v, want %v",
					tt.explicit, tt.projectRepo, tt.touched, tt.wfRepos, got, tt.want)
			}
		})
	}
}
