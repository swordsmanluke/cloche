package scan

import (
	_ "embed"

	"github.com/swordsmanluke/cloche/internal/domain"
)

//go:embed prompts/discover-domains.md
var discoverDomainsPrompt string

//go:embed prompts/extract.md
var extractPrompt string

//go:embed prompts/reconcile.md
var reconcilePrompt string

// collectSourcesScript forwards CLOCHE_INTENT_FULL — set by
// intentScanWithClient's RunWorkflowRequest.Env for `cloche intent scan
// --full`, never via the run prompt — as an explicit --full flag to
// collect-sources, which resets its scan-state cursors for this run.
const collectSourcesScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
OUT="$TEMP/intent-scan-sources"
FULL_FLAG=""
if [ -n "${CLOCHE_INTENT_FULL:-}" ]; then
  FULL_FLAG="--full"
fi
cloche intent collect-sources --project "$PROJECT_DIR" --out "$OUT" $FULL_FLAG
cloche set intent_scan_sources_dir "$OUT"
`

const applyReconcileScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
RECONCILE_FILE="$TEMP/reconcile.json"
if [ ! -f "$RECONCILE_FILE" ]; then
  echo "error: $RECONCILE_FILE not found — did the reconcile step write it?" >&2
  exit 1
fi
cloche intent apply-reconcile --project "$PROJECT_DIR" --reconcile-file "$RECONCILE_FILE"
`

// commitScript commits any changes apply-reconcile wrote under
// .cloche/intent/, scoped strictly to that path so unrelated working-tree
// edits present when the scan fires are left untouched.
//
// It operates against CLOCHE_PROJECT_DIR — the same directory
// apply-reconcile just wrote to, which is what makes the pair correct. Note
// that CLOCHE_PROJECT_DIR is the project directory, NOT necessarily the main
// git worktree: a host script step's working directory defaults to the main
// worktree, but CLOCHE_PROJECT_DIR keeps pointing at the actual project dir,
// and the two differ whenever the project dir is a linked worktree. Both are
// defaults rather than invariants — Executor.scriptDir() falls back to
// ProjectDir when MainDir is unset, and MainWorktreeDir() falls back to the
// project dir on any error. Nothing here should assume which tree it is in;
// correctness comes from committing in the same directory that was written.
const commitScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
INTENT_DIR=".cloche/intent"

# git diff --quiet alone misses brand-new requirement files (they're
# untracked, not modified), so use status --porcelain to catch both.
STATUS=$(git -C "$PROJECT_DIR" status --porcelain -- "$INTENT_DIR")
if [ -z "$STATUS" ]; then
  echo "intent scan: no changes under $INTENT_DIR, nothing to commit"
  exit 0
fi

SUMMARY="requirements updated"
if [ -n "${CLOCHE_PREV_OUTPUT:-}" ] && [ -s "$CLOCHE_PREV_OUTPUT" ]; then
  SUMMARY=$(tail -n 1 "$CLOCHE_PREV_OUTPUT")
fi
if echo "$STATUS" | grep -q "domains.yaml"; then
  SUMMARY="$SUMMARY; domains.yaml updated"
fi

export GIT_AUTHOR_NAME="${CLOCHE_GIT_AUTHOR_NAME:-cloche}"
export GIT_AUTHOR_EMAIL="${CLOCHE_GIT_AUTHOR_EMAIL:-cloche@local}"
export GIT_COMMITTER_NAME="${CLOCHE_GIT_AUTHOR_NAME:-cloche}"
export GIT_COMMITTER_EMAIL="${CLOCHE_GIT_AUTHOR_EMAIL:-cloche@local}"

# A concurrent second scan, or a container-authored merge-to-base step, can
# momentarily hold the index lock in the main worktree; retry a few times
# rather than leaving a half-staged index.
attempt=0
while [ "$attempt" -lt 5 ]; do
  if git -C "$PROJECT_DIR" add -- "$INTENT_DIR"; then
    break
  fi
  attempt=$((attempt + 1))
  sleep 1
done
if [ "$attempt" -ge 5 ]; then
  echo "error: git add $INTENT_DIR failed after retries (index lock contention?)" >&2
  exit 1
fi

if git -C "$PROJECT_DIR" diff --cached --quiet -- "$INTENT_DIR"; then
  echo "intent scan: no staged changes under $INTENT_DIR, nothing to commit"
  exit 0
fi

attempt=0
while [ "$attempt" -lt 5 ]; do
  if git -C "$PROJECT_DIR" commit -m "intent scan: $SUMMARY" -- "$INTENT_DIR"; then
    echo "intent scan: committed $INTENT_DIR ($SUMMARY)"
    exit 0
  fi
  attempt=$((attempt + 1))
  sleep 1
done
echo "error: git commit $INTENT_DIR failed after retries (index lock contention?)" >&2
exit 1
`

// BuiltinWorkflow returns a freshly constructed intent-scan host workflow.
// Every call allocates new maps so callers (e.g. ResolveAgents) can mutate
// the result without affecting other resolutions.
func BuiltinWorkflow() *domain.Workflow {
	return &domain.Workflow{
		Name:     "intent-scan",
		Location: domain.LocationHost,
		Builtin:  true,
		Config: map[string]string{
			"_location_block":    "host",
			"host.agent_command": "claude",
		},
		EntryStep: "discover-domains",
		Steps: map[string]*domain.Step{
			"discover-domains": {
				Name: "discover-domains",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  discoverDomainsPrompt,
					"timeout": "10m",
				},
				Results: []string{"success", "fail"},
			},
			"collect-sources": {
				Name: "collect-sources",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": collectSourcesScript,
				},
				Results: []string{"success", "none", "fail"},
			},
			"extract": {
				Name: "extract",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  extractPrompt,
					"timeout": "20m",
				},
				Results: []string{"success", "fail"},
			},
			"reconcile": {
				Name: "reconcile",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  reconcilePrompt,
					"timeout": "20m",
				},
				Results: []string{"success", "fail"},
			},
			"apply-reconcile": {
				Name: "apply-reconcile",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": applyReconcileScript,
				},
				Results: []string{"success", "fail"},
			},
			"commit": {
				Name: "commit",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": commitScript,
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "discover-domains", Result: "success", To: "collect-sources"},
			{From: "discover-domains", Result: "fail", To: domain.StepAbort},
			{From: "collect-sources", Result: "success", To: "extract"},
			{From: "collect-sources", Result: "none", To: domain.StepDone},
			{From: "collect-sources", Result: "fail", To: domain.StepAbort},
			{From: "extract", Result: "success", To: "reconcile"},
			{From: "extract", Result: "fail", To: domain.StepAbort},
			{From: "reconcile", Result: "success", To: "apply-reconcile"},
			{From: "reconcile", Result: "fail", To: domain.StepAbort},
			{From: "apply-reconcile", Result: "success", To: "commit"},
			{From: "apply-reconcile", Result: "fail", To: domain.StepAbort},
			{From: "commit", Result: "success", To: domain.StepDone},
			{From: "commit", Result: "fail", To: domain.StepAbort},
		},
	}
}
