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

//go:embed prompts/repair-domains.md
var repairDomainsPrompt string

//go:embed prompts/repair-candidates.md
var repairCandidatesPrompt string

//go:embed prompts/repair-reconcile.md
var repairReconcilePrompt string

// checkCandidatesScript turns the extract agent's candidates.csv into
// candidates.json (`cloche intent check-candidates`): "missing" when the
// agent reported success without writing the file, fail with a problem list
// when it is malformed, success with the JSON written otherwise.
const checkCandidatesScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
REPO=$(cloche get intent_scan_repo 2>/dev/null || true)
cloche intent check-candidates --project "$PROJECT_DIR" --repo "$REPO" --csv "$TEMP/candidates.csv" --out "$TEMP/candidates.json"
`

// checkDomainsScript turns the discover-domains agent's domains.csv into
// domains.yaml via `cloche intent apply-domains`, which validates the CSV
// and marshals the YAML itself. On a validation failure its stdout lists the
// problems and becomes the repair-domains step's {{ $prev_output }}.
const checkDomainsScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
cloche intent apply-domains --project "$PROJECT_DIR" --csv "$TEMP/domains.csv"
`

// nextPassScript advances the scan to its next pass (see Collection.Passes):
// `cloche intent next-pass` reads the passes collect-sources listed, points
// the intent_scan_repo / intent_scan_sources_dir KV keys at the next one,
// clears the previous pass's hand-off files from $TEMP, and reports "pass";
// when none remain it prints the aggregated apply summary and reports
// "done". Each pass then runs the intent-scan-repo sub-workflow, so one
// repo's extract/reconcile never sees another's material or requirements.
const nextPassScript = `set -eu
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
OUT=$(cloche get intent_scan_sources_root)
cloche intent next-pass --sources "$OUT" --cursor "$TEMP/intent-scan-pass" --temp "$TEMP"
`

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
cloche set intent_scan_sources_root "$OUT"
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
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
REPO=$(cloche get intent_scan_repo 2>/dev/null || true)
cloche intent check-reconcile --project "$PROJECT_DIR" --repo "$REPO" --csv "$TEMP/reconcile.csv" --candidates-file "$TEMP/candidates.json" --out "$TEMP/reconcile.json"
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
REPO=$(cloche get intent_scan_repo 2>/dev/null || true)
cloche intent apply-reconcile --project "$PROJECT_DIR" --repo "$REPO" --reconcile-file "$TEMP/reconcile.json" --candidates-file "$TEMP/candidates.json" --summary-file "$TEMP/scan-summary.txt"
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
INDEX_DIR=".cloche/intent-index"

untrack_index_dir() {
  export GIT_AUTHOR_NAME="${CLOCHE_GIT_AUTHOR_NAME:-cloche}"
  export GIT_AUTHOR_EMAIL="${CLOCHE_GIT_AUTHOR_EMAIL:-cloche@local}"
  export GIT_COMMITTER_NAME="${CLOCHE_GIT_AUTHOR_NAME:-cloche}"
  export GIT_COMMITTER_EMAIL="${CLOCHE_GIT_AUTHOR_EMAIL:-cloche@local}"
  TMP_INDEX=$(mktemp)
  if GIT_INDEX_FILE="$TMP_INDEX" git -C "$PROJECT_DIR" read-tree HEAD \
     && GIT_INDEX_FILE="$TMP_INDEX" git -C "$PROJECT_DIR" rm -r -q --cached -- "$INDEX_DIR" \
     && TREE=$(GIT_INDEX_FILE="$TMP_INDEX" git -C "$PROJECT_DIR" write-tree) \
     && NEW=$(git -C "$PROJECT_DIR" commit-tree "$TREE" -p HEAD -m "intent: stop tracking the derived embedding index under $INDEX_DIR") \
     && git -C "$PROJECT_DIR" update-ref HEAD "$NEW" \
     && git -C "$PROJECT_DIR" rm -r -q -f --cached -- "$INDEX_DIR"; then
    echo "intent scan: untracked derived index under $INDEX_DIR (it is ignored from now on)"
  else
    echo "warning: could not untrack $INDEX_DIR; it will keep showing as modified" >&2
  fi
  rm -f "$TMP_INDEX"
}

commit_intent_dir() {
  SUMMARY="$1"

  # The embedding index under $INDEX_DIR is a derived cache the daemon
  # rewrites on every step's injection; it is meant to be ignored, never
  # committed. A project that committed it before the index became
  # self-ignoring keeps showing vectors.json as modified after every task
  # run (.gitignore has no effect on a tracked file), so untrack it here,
  # the one place cloche already commits intent state. The removal is its
  # own commit built from a scratch index: "git commit -- <path>" (used
  # below) commits the working-tree contents of <path>, which would put
  # the still-present vectors.json straight back.
  if [ -n "$(git -C "$PROJECT_DIR" ls-files -- "$INDEX_DIR")" ]; then
    untrack_index_dir
  fi

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

  # Never commit a store the daemon cannot load: that silently disables
  # injection for the whole project while every later scan reports success.
  # Put the intent dir back to its last committed state instead, and fail
  # loudly.
  if ! cloche intent validate --project "$PROJECT_DIR"; then
    echo "ERROR intent: store failed validation; reverting $INTENT_DIR to its last committed state and discarding this scan's output" >&2
    git -C "$PROJECT_DIR" checkout -- "$INTENT_DIR" 2>/dev/null || true
    git -C "$PROJECT_DIR" clean -fdq -- "$INTENT_DIR" 2>/dev/null || true
    exit 1
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
// The summary comes from next-pass's closing "created N, ..." line, which
// aggregates every pass's apply-reconcile report; when no pass applied
// anything (collect-sources found nothing, or every pass reconciled to
// nothing) the last line is not a summary, so fall back to a fixed message.
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
//
// The scan is a loop over passes — one per configured repository with new
// material, then the project root (see Collection.Passes). Each pass runs
// the intent-scan-repo sub-workflow (BuiltinRepoWorkflow) as a fresh engine
// run, which is what lets its agent steps keep their own max_attempts
// budget per pass; an in-workflow cycle back to extract would exhaust them
// on the second repo.
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
				Results: []string{"success", "none", "fail"},
			},
			"check-domains": {
				Name: "check-domains",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": checkDomainsScript,
				},
				Results: []string{"success", "fail"},
			},
			"repair-domains": {
				Name: "repair-domains",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":       repairDomainsPrompt,
					"timeout":      "5m",
					"max_attempts": "2",
				},
				Results: []string{"success", "fail", "give-up"},
			},
			"collect-sources": {
				Name: "collect-sources",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": collectSourcesScript,
				},
				Results: []string{"success", "none", "fail"},
			},
			"next-pass": {
				Name: "next-pass",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": nextPassScript,
				},
				Results: []string{"pass", "done", "fail"},
			},
			"scan-pass": {
				Name: "scan-pass",
				Type: domain.StepTypeWorkflow,
				Config: map[string]string{
					"workflow_name": RepoWorkflowName,
				},
				Results: []string{"success", "fail", "timeout"},
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
			// discover-domains hands off a CSV; check-domains (a script)
			// validates it and writes domains.yaml; a validation failure goes
			// to repair-domains, which fixes the CSV's format only — bounded
			// by its max_attempts — and comes back to the same check.
			{From: "discover-domains", Result: "success", To: "check-domains"},
			{From: "discover-domains", Result: "none", To: "collect-sources"},
			{From: "discover-domains", Result: "fail", To: "abort-cleanup"},
			{From: "check-domains", Result: "success", To: "collect-sources"},
			{From: "check-domains", Result: "fail", To: "repair-domains"},
			{From: "repair-domains", Result: "success", To: "check-domains"},
			{From: "repair-domains", Result: "fail", To: "abort-cleanup"},
			{From: "repair-domains", Result: "give-up", To: "abort-cleanup"},
			// The pass loop: next-pass picks the next source tree with new
			// material and scan-pass runs the per-repo sub-workflow on it;
			// when none remain, commit. A failed pass aborts the whole scan
			// (abort-cleanup reverts the cursors so nothing is skipped).
			{From: "collect-sources", Result: "success", To: "next-pass"},
			{From: "collect-sources", Result: "none", To: "commit"},
			{From: "collect-sources", Result: "fail", To: "abort-cleanup"},
			{From: "next-pass", Result: "pass", To: "scan-pass"},
			{From: "next-pass", Result: "done", To: "commit"},
			{From: "next-pass", Result: "fail", To: "abort-cleanup"},
			{From: "scan-pass", Result: "success", To: "next-pass"},
			{From: "scan-pass", Result: "fail", To: "abort-cleanup"},
			{From: "scan-pass", Result: "timeout", To: "abort-cleanup"},
			{From: "commit", Result: "success", To: domain.StepDone},
			{From: "commit", Result: "fail", To: domain.StepAbort},
			// Both outcomes abort: cleanup never turns a failed scan into a
			// success, and FindFirstFailedStep still names the real culprit.
			{From: "abort-cleanup", Result: "success", To: domain.StepAbort},
			{From: "abort-cleanup", Result: "fail", To: domain.StepAbort},
		},
	}
}

// RepoWorkflowName is the name of the per-pass sub-workflow intent-scan
// dispatches for each source tree.
const RepoWorkflowName = "intent-scan-repo"

// BuiltinRepoWorkflow returns the sub-workflow intent-scan runs once per
// pass: extract candidates from that pass's material, reconcile them
// against the requirements visible to that repo, and apply. It reads the
// pass from the intent_scan_repo / intent_scan_sources_dir KV keys that
// next-pass sets and is not meant to be run on its own.
func BuiltinRepoWorkflow() *domain.Workflow {
	return &domain.Workflow{
		Name:     RepoWorkflowName,
		Location: domain.LocationHost,
		Builtin:  true,
		Config: map[string]string{
			"_location_block":    "host",
			"host.agent_command": "claude",
		},
		EntryStep: "extract",
		Steps: map[string]*domain.Step{
			"extract": {
				Name: "extract",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":       extractPrompt,
					"timeout":      "20m",
					"max_attempts": "2",
				},
				Results: []string{"success", "fail", "give-up"},
			},
			"check-candidates": {
				Name: "check-candidates",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": checkCandidatesScript,
				},
				Results: []string{"success", "missing", "fail"},
			},
			"repair-candidates": {
				Name: "repair-candidates",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":       repairCandidatesPrompt,
					"timeout":      "10m",
					"max_attempts": "2",
				},
				Results: []string{"success", "fail", "give-up"},
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
				Results: []string{"success", "missing", "fail"},
			},
			"repair-reconcile": {
				Name: "repair-reconcile",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":       repairReconcilePrompt,
					"timeout":      "10m",
					"max_attempts": "2",
				},
				Results: []string{"success", "fail", "give-up"},
			},
			"apply-reconcile": {
				Name: "apply-reconcile",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": applyReconcileScript,
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			// Each agent hand-off is a CSV checked by a script. "missing"
			// (the agent reported success without writing the file) re-runs
			// the agent step; "fail" (malformed CSV or a hard-rule violation)
			// goes to a format-only repair step bounded by its max_attempts.
			// The check steps' wires back into extract/reconcile are listed
			// before the forward wires on purpose: the host executor forwards
			// the output of the first inbound wire whose step has run, so a
			// re-run sees the check's message rather than the earlier step's
			// summary again.
			{From: "check-candidates", Result: "missing", To: "extract"},
			{From: "extract", Result: "success", To: "check-candidates"},
			{From: "extract", Result: "fail", To: domain.StepAbort},
			{From: "extract", Result: "give-up", To: domain.StepAbort},
			{From: "check-candidates", Result: "fail", To: "repair-candidates"},
			{From: "repair-candidates", Result: "success", To: "check-candidates"},
			{From: "repair-candidates", Result: "fail", To: domain.StepAbort},
			{From: "repair-candidates", Result: "give-up", To: domain.StepAbort},
			{From: "check-reconcile", Result: "missing", To: "reconcile"},
			{From: "check-candidates", Result: "success", To: "reconcile"},
			{From: "reconcile", Result: "success", To: "check-reconcile"},
			{From: "reconcile", Result: "none", To: domain.StepDone},
			{From: "reconcile", Result: "fail", To: domain.StepAbort},
			{From: "reconcile", Result: "give-up", To: domain.StepAbort},
			{From: "check-reconcile", Result: "success", To: "apply-reconcile"},
			{From: "check-reconcile", Result: "fail", To: "repair-reconcile"},
			{From: "repair-reconcile", Result: "success", To: "check-reconcile"},
			{From: "repair-reconcile", Result: "fail", To: domain.StepAbort},
			{From: "repair-reconcile", Result: "give-up", To: domain.StepAbort},
			{From: "apply-reconcile", Result: "success", To: domain.StepDone},
			{From: "apply-reconcile", Result: "fail", To: domain.StepAbort},
		},
	}
}
