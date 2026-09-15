// Package promptrev resolves the prompt file a workflow step's `prompt`
// config references and the git revision of that file, so the ledger view
// (see internal/adapters/web/handler_ledger.go) can join agent-step attempts
// to the prompt content they actually ran with.
package promptrev

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/builtin"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
)

// fileRefRegex matches the DSL's file("...") config-value syntax used for
// step prompt templates, e.g. `prompt = file(".cloche/prompts/implement.md")`.
var fileRefRegex = regexp.MustCompile(`^file\("(.+)"\)$`)

// ResolveFile returns the project-relative path a step's raw `prompt`
// config value references via file("..."), and whether it references a file
// at all (as opposed to an inline prompt string or no prompt config).
func ResolveFile(promptConfig string) (path string, ok bool) {
	m := fileRefRegex.FindStringSubmatch(promptConfig)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// GitRevision returns the commit SHA that last touched relPath as of HEAD in
// dir, or "" if unavailable (not a git repo, untracked file, git not
// installed, etc). Best-effort: callers treat "" as "unknown".
func GitRevision(dir, relPath string) string {
	return GitRevisionAt(dir, relPath, time.Time{})
}

// GitRevisionAt returns the commit SHA that last touched relPath at or
// before the given time (or as of HEAD, when at is zero), or "" if
// unavailable.
func GitRevisionAt(dir, relPath string, at time.Time) string {
	args := []string{"log", "-1", "--format=%H"}
	if !at.IsZero() {
		args = append(args, "--until="+at.Format(time.RFC3339))
	}
	args = append(args, "--", relPath)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ResolveWorkflowPromptFiles scans every step of the named workflow (project
// .cloche files first, falling back to built-ins) and returns a map of step
// name to the project-relative prompt file it references, for steps whose
// `prompt` config is a file("...") reference. Used to backfill prompt
// revisions for attempts that predate live dispatch-time recording.
func ResolveWorkflowPromptFiles(dir, workflowName string) map[string]string {
	wf := lookupWorkflow(dir, workflowName)
	if wf == nil {
		return nil
	}
	files := map[string]string{}
	for _, step := range wf.Steps {
		if promptConfig, ok := step.Config["prompt"]; ok {
			if relPath, ok := ResolveFile(promptConfig); ok {
				files[step.Name] = relPath
			}
		}
	}
	return files
}

// lookupWorkflow finds a workflow by name, scanning a project's .cloche
// files first and falling back to built-ins. Returns nil if not found.
func lookupWorkflow(dir, workflowName string) *domain.Workflow {
	clocheDir := filepath.Join(dir, ".cloche")
	entries, _ := os.ReadDir(clocheDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(clocheDir, e.Name()))
		if err != nil {
			continue
		}
		wfs, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		if found, ok := wfs[workflowName]; ok {
			return found
		}
	}
	if found, ok := builtin.Lookup(workflowName); ok {
		return found
	}
	return nil
}
