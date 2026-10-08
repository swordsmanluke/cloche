package stepinput

import (
	"strings"
	"testing"
)

func TestScript_RecordsClocheValuesButOnlyOtherNames(t *testing.T) {
	got := Script("go test ./...\n", []string{
		"GITHUB_TOKEN=ghp_secret",
		"PATH=/usr/bin",
		"CLOCHE_RUN_ID=abc-develop",
		"CLOCHE_PROJECT_DIR=/p",
		"PATH=/usr/bin",
	}, []string{"child_branch", "task_title", "child_branch"})

	for _, want := range []string{
		"command:\n  go test ./...\n",
		"CLOCHE_PROJECT_DIR=/p\n  CLOCHE_RUN_ID=abc-develop\n",
		"names only):\n  GITHUB_TOKEN PATH\n",
		"kv keys:\n  child_branch task_title\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ghp_secret") || strings.Contains(got, "/usr/bin") {
		t.Errorf("non-Cloche env values must not be recorded:\n%s", got)
	}
}

func TestScript_Empty(t *testing.T) {
	got := Script("", nil, nil)
	if !strings.Contains(got, "command:\n  (empty)") || strings.Count(got, "(none)") != 3 {
		t.Errorf("unexpected rendering:\n%s", got)
	}
}

func TestPoll(t *testing.T) {
	got := Poll("30s", "gh pr view 12 --json state\n")
	if got != "interval: 30s\ncondition:\n  gh pr view 12 --json state\n" {
		t.Errorf("got %q", got)
	}
}
