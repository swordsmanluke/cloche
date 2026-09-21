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

// checkReconcileScript verifies the reconcile agent actually produced its
// output before anything downstream trusts its "success": an agent that
// claims success without writing reconcile.json is routed back to reconcile
// (bounded by that step's max_attempts) instead of failing the whole scan in
// apply-reconcile. Its stdout becomes the retried reconcile step's
// "## User Request" input, so the message is written to the agent. A retry
// that finds candidates.json empty should report "none", which ends the
// workflow without coming back here.
const checkReconcileScript = `set -eu
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
if [ -s "$TEMP/reconcile.json" ]; then
  echo "reconcile.json present"
  exit 0
fi
echo "Your previous attempt reported success, but $TEMP/reconcile.json does not exist or is empty."
echo "Do the reconcile work now and write that file before reporting success."
echo "If $TEMP/candidates.json has an empty candidates array, report none instead."
exit 1
`

// applyReconcileScript delegates the missing-file decision to `cloche intent
// apply-reconcile` itself (see cmdIntentApplyReconcile in cmd/cloche/
// intent.go) rather than failing here on a bare `[ -f "$RECONCILE_FILE" ]`
// check: a reconcile.json missing because extract found zero candidates
// (candidates.json has an empty list) is a no-op success, not a failure —
// only a missing reconcile.json alongside a non-empty candidates.json is a
// real step failure (the reconcile agent claimed success without writing
// its output).
const applyReconcileScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
cloche intent apply-reconcile --project "$PROJECT_DIR" --reconcile-file "$TEMP/reconcile.json" --candidates-file "$TEMP/candidates.json"
`

// commitIntentDirFn is the shared shell body of the commit and abort-cleanup
// steps: `commit_intent_dir "<summary>"` commits any changes under
// .cloche/intent/, scoped strictly to that path so unrelated working-tree
// edits present when the scan fires are left untouched. It exits the script
// itself (0 on commit or clean tree, 1 on persistent git failure).
//
// It operates against CLOCHE_PROJECT_DIR — the same directory the scan's
// earlier steps wrote to, which is what makes the pair correct. Note that
// CLOCHE_PROJECT_DIR is the project directory, NOT necessarily the main git
// worktree: a host script step's working directory defaults to the main
// worktree, but CLOCHE_PROJECT_DIR keeps pointing at the actual project dir,
// and the two differ whenever the project dir is a linked worktree. Both are
// defaults rather than invariants — Executor.scriptDir() falls back to
// ProjectDir when MainDir is unset, and MainWorktreeDir() falls back to the
// project dir on any error. Nothing here should assume which tree it is in;
// correctness comes from committing in the same directory that was written.
const commitIntentDirFn = `PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
INTENT_DIR=".cloche/intent"

commit_intent_dir() {
  SUMMARY="$1"

  # git diff --quiet alone misses brand-new requirement files (they're
  # untracked, not modified), so use status --porcelain to catch both.
  STATUS=$(git -C "$PROJECT_DIR" status --porcelain -- "$INTENT_DIR")
  if [ -z "$STATUS" ]; then
    echo "intent scan: no changes under $INTENT_DIR, nothing to commit"
    exit 0
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
}
`

// commitScript is the scan's terminal step on every non-failing path —
// including the `none` exits from collect-sources and reconcile, which
// still leave scan-state.yaml (advanced by collect-sources) and possibly
// domains.yaml (rewritten by discover-domains) modified. Anything the scan
// leaves uncommitted dirties the main worktree and blocks later merges.
//
// The summary comes from apply-reconcile's "created N, ..." line when that
// step ran; on a `none` path the predecessor is a script or an agent whose
// last log line is not a summary, so fall back to a fixed message.
const commitScript = `set -eu
` + commitIntentDirFn + `
SUMMARY="scan state updated"
if [ -n "${CLOCHE_PREV_OUTPUT:-}" ] && [ -s "$CLOCHE_PREV_OUTPUT" ]; then
  LAST=$(tail -n 1 "$CLOCHE_PREV_OUTPUT")
  case "$LAST" in
    created\ *) SUMMARY="$LAST" ;;
  esac
fi
commit_intent_dir "$SUMMARY"
`

// abortCleanupScript runs on every failing path before the run aborts. It
// puts scan-state.yaml back the way it was — collect-sources advances the
// cursors as soon as it runs, so committing them after a failed extract or
// reconcile would silently skip that window on the next scan — then commits
// whatever else the scan legitimately produced (domains.yaml from
// discover-domains) so the worktree is left clean either way.
const abortCleanupScript = `set -eu
` + commitIntentDirFn + `
STATE="$INTENT_DIR/scan-state.yaml"
if git -C "$PROJECT_DIR" ls-files --error-unmatch -- "$STATE" >/dev/null 2>&1; then
  git -C "$PROJECT_DIR" checkout -- "$STATE"
  echo "intent scan: reverted $STATE (scan aborted; window will be rescanned)"
elif [ -f "$PROJECT_DIR/$STATE" ]; then
  rm -f "$PROJECT_DIR/$STATE"
  echo "intent scan: removed untracked $STATE (scan aborted)"
fi
commit_intent_dir "partial results (scan aborted)"
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
					"prompt":       reconcilePrompt,
					"timeout":      "20m",
					"max_attempts": "3",
				},
				Results: []string{"success", "none", "fail", "give-up"},
			},
			"check-reconcile": {
				Name: "check-reconcile",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": checkReconcileScript,
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
			"abort-cleanup": {
				Name: "abort-cleanup",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": abortCleanupScript,
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "discover-domains", Result: "success", To: "collect-sources"},
			{From: "discover-domains", Result: "fail", To: "abort-cleanup"},
			{From: "collect-sources", Result: "success", To: "extract"},
			{From: "collect-sources", Result: "none", To: "commit"},
			{From: "collect-sources", Result: "fail", To: "abort-cleanup"},
			// check-reconcile's wire into reconcile is listed before extract's
			// on purpose: the host executor forwards the output of the first
			// inbound wire whose step has run, so a retried reconcile sees
			// check-reconcile's "you didn't write the file" message rather
			// than extract's summary again.
			{From: "check-reconcile", Result: "fail", To: "reconcile"},
			{From: "extract", Result: "success", To: "reconcile"},
			{From: "extract", Result: "fail", To: "abort-cleanup"},
			{From: "reconcile", Result: "success", To: "check-reconcile"},
			{From: "reconcile", Result: "none", To: "commit"},
			{From: "reconcile", Result: "fail", To: "abort-cleanup"},
			{From: "reconcile", Result: "give-up", To: "abort-cleanup"},
			{From: "check-reconcile", Result: "success", To: "apply-reconcile"},
			{From: "apply-reconcile", Result: "success", To: "commit"},
			{From: "apply-reconcile", Result: "fail", To: "abort-cleanup"},
			{From: "commit", Result: "success", To: domain.StepDone},
			{From: "commit", Result: "fail", To: domain.StepAbort},
			// Both outcomes abort: cleanup never turns a failed scan into a
			// success, and FindFirstFailedStep still names the real culprit.
			{From: "abort-cleanup", Result: "success", To: domain.StepAbort},
			{From: "abort-cleanup", Result: "fail", To: domain.StepAbort},
		},
	}
}
