package signal

import (
	_ "embed"
	"strings"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// The default handle-notification workflow does the least useful thing: no
// thresholds, no muting, no model step. A project replaces it by defining its
// own host workflow of the same name.
//
//	lookup --found--> comment ----> done
//	lookup --none---> create  ----> done
//
// Steps are POSIX sh (host scripts run under `sh -c`) and reach the tracker
// through `cloche tasks find|update|create`, i.e. whichever module
// project.cloche binds to update-task / create-task.

// lookupScript finds an open task carrying this source and key (update-task
// "find"). Not finding one is the "none" result, not a failure.
const lookupScript = `set -u
err=$(mktemp) || exit 1
trap 'rm -f "$err"' EXIT
if id=$(cloche tasks find --project "${CLOCHE_PROJECT_DIR:-.}" \
    --source "$(cloche get signal_source)" --key "$(cloche get signal_key)" 2>"$err"); then
  cloche set found_task_id "$id"
  echo "open task $id already carries this source and key"
  echo "CLOCHE_RESULT:found"
  exit 0
fi
if grep -q "no open task" "$err"; then
  echo "no open task for this source and key"
  echo "CLOCHE_RESULT:none"
  exit 0
fi
cat "$err" >&2
exit 1
`

// commentScript adds this occurrence to the task lookup found.
const commentScript = `set -eu
id=$(cloche get found_task_id)
count=$(cloche get signal_count 2>/dev/null || echo 1)
evidence=$(cloche get signal_evidence 2>/dev/null || true)
comment="Seen again: $(cloche get signal_title)
Source: $(cloche get signal_source) / $(cloche get signal_key)
Severity: $(cloche get signal_severity)
Occurrences in this burst: $count
At: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
if [ -n "$evidence" ]; then
  comment="$comment

$evidence"
fi
cloche tasks update "$id" --project "${CLOCHE_PROJECT_DIR:-.}" --comment "$comment"
`

// createScript creates a task from the signal's title and evidence as they
// are, priority from severity, domain (if any) as a label. The source and key
// are recorded on the task so the next occurrence finds it.
const createScript = `set -eu
evidence=$(cloche get signal_evidence 2>/dev/null || true)
domain=$(cloche get signal_domain 2>/dev/null || true)
set -- tasks create --project "${CLOCHE_PROJECT_DIR:-.}" \
  --title "$(cloche get signal_title)" \
  --priority "$(cloche get signal_priority)" \
  --source "$(cloche get signal_source)" --key "$(cloche get signal_key)"
if [ -n "$evidence" ]; then set -- "$@" --body "$evidence"; fi
if [ -n "$domain" ]; then set -- "$@" --label "$domain"; fi
id=$(cloche "$@")
echo "created task $id"
`

// BuiltinWorkflow returns the default handle-notification workflow.
func BuiltinWorkflow() *domain.Workflow {
	script := func(name, run string, results ...string) *domain.Step {
		return &domain.Step{
			Name:    name,
			Type:    domain.StepTypeScript,
			Config:  map[string]string{"run": run},
			Results: results,
		}
	}
	return &domain.Workflow{
		Name:     WorkflowName,
		Location: domain.LocationHost,
		Builtin:  true,
		Config: map[string]string{
			"_location_block": "host",
		},
		EntryStep: "lookup",
		Steps: map[string]*domain.Step{
			"lookup":  script("lookup", lookupScript, "found", "none", "fail"),
			"comment": script("comment", commentScript, "success", "fail"),
			"create":  script("create", createScript, "success", "fail"),
		},
		Wiring: []domain.Wire{
			{From: "lookup", Result: "found", To: "comment"},
			{From: "lookup", Result: "none", To: "create"},
			{From: "lookup", Result: "fail", To: domain.StepAbort},
			{From: "comment", Result: "success", To: domain.StepDone},
			{From: "comment", Result: "fail", To: domain.StepAbort},
			{From: "create", Result: "success", To: domain.StepDone},
			{From: "create", Result: "fail", To: domain.StepAbort},
		},
	}
}

// JudgeWorkflowName is the host workflow `cloche questions ask` starts for
// freshly asked questions. A project replaces the built-in by defining a host
// workflow of the same name, exactly as with handle-notification.
const JudgeWorkflowName = "judge"

//go:embed judge_rules.md
var judgeRules string

//go:embed judge_reporting.md
var judgeReporting string

// JudgePrompt is the judge's prompt: the generic rules, then the bound qa
// module's judge.md (how to read and answer its questions), then the ids to
// judge and the result-marker instructions.
func JudgePrompt(moduleGuide string, questionIDs []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(judgeRules))
	if g := strings.TrimSpace(moduleGuide); g != "" {
		b.WriteString("\n\n## This project's qa module\n\n")
		b.WriteString(g)
	}
	if len(questionIDs) > 0 {
		b.WriteString("\n\n## Questions to judge\n\n")
		for _, id := range questionIDs {
			b.WriteString("- " + id + "\n")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(judgeReporting))
	b.WriteString("\n")
	return b.String()
}

// JudgeStep is the name of the judge workflow's agent step.
const JudgeStep = "judge"

// BuiltinJudgeWorkflow returns the default judge workflow: one agent step. Its
// prompt is the rules alone; the launcher fills in the module's judge.md and
// the question ids (see host.RunJudge) before running it.
func BuiltinJudgeWorkflow() *domain.Workflow {
	return &domain.Workflow{
		Name:     JudgeWorkflowName,
		Location: domain.LocationHost,
		Builtin:  true,
		Config: map[string]string{
			"_location_block":    "host",
			"host.agent_command": "claude",
		},
		EntryStep: JudgeStep,
		Steps: map[string]*domain.Step{
			JudgeStep: {
				Name: JudgeStep,
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  JudgePrompt("", nil),
					"timeout": "15m",
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: JudgeStep, Result: "success", To: domain.StepDone},
			{From: JudgeStep, Result: "fail", To: domain.StepAbort},
		},
	}
}
