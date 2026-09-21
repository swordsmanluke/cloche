# Intent Tracking A/B Pilot — Results (2026-09-17)

**Status:** Pilot complete (n=1/arm). Protocol: [`2026-09-14-intent-ab-experiment-protocol.md`](../../2026-09-14-intent-ab-experiment-protocol.md).

## Executor substitution (deviation from the frozen protocol)

The protocol specifies bonsai-8b-16k as executor. It was dropped before any
scored run, across three independent harnesses, each ruled out for a
different reason:

1. **Hand-rolled `agent_command` wrapper (E4)** — the wrapper never inlined
   `DESIGN.md` into the prompt, so bonsai got empty completions on every real
   task. Fixed, then found a second, harder-to-fix failure mode: on the trivial
   E4 fixture task, bonsai wrote textbook-correct code in 3/3 trials but
   packaged it wrong every time (echoed the wrapper's literal example
   placeholder path, or a bare `​```python` language tag instead of a real
   path) — a structural instruction-following failure, not a coding one.
2. **opencode (real agentic tool) + bonsai via Ollama** — infrastructure
   fully verified working (dispatch, network, tool-calling config), but on
   the real first pilot task bonsai's own reasoning trace showed it believed
   its tool set was "Read, Glob, Grep, Webfetch, Write" — no Bash — and so
   never executed the `cat`/`clo get` command needed to even read the task
   description. Never established whether this is an opencode↔Ollama
   tool-calling gap or bonsai misreporting its own tools.
3. Both failures are consistent with the prior [intent-retrieval
   spike](../2026-09-13-intent-retrieval/README.md)'s own conclusion: "local
   reasoning-tier models (1-bit bonsai) are impractical generators... slow,
   prone to empty/refusal outputs."

**Substituted executor: `claude-haiku-4-5-20251001`**, driven through
Cloche's native `claude` agent adapter (no custom harness) for both arms.
This changes what the pilot measures: no longer "does injection help a
*weak* executor" (the original question), but "does injection change
what a *capable* executor produces, and does either arm clear a difficult,
anti-prior task list at all." Still directly useful groundwork, but the
calibration/headroom implications below should be read with this
substitution in mind before committing to bonsai-scale replications.

## What was run

- Both arms: same frozen `seed-v1`, same 12-task list (with D1–D5 embedded
  verbatim per the protocol), same executor (`claude-haiku-4-5-20251001`,
  same `agent_args` in both), pilot of 1 run/arm.
- Arm A: `intent.scan_after_tasks = false`, no `.cloche/intent/` ever created
  (contamination check passed).
- Arm B: `intent.scan_after_tasks = true`, `intent.token_budget = 1000`,
  dispatched through the real orchestration loop (`main`/`develop`, not a
  differently-named workflow) so the daemon's post-task auto-scan trigger
  — which is wired specifically to the literal `main` workflow name — fired
  correctly. First scan bootstrapped `.cloche/intent/` after task 1; 29
  requirements existed by the end of the run.
- Both arms required manual recovery for a subset of tasks: an intermittent
  bug (not model quality — see Process metrics) where Claude prints the bare
  `CLOCHE_RESULT:success` instead of the required nonced form after a long
  response, which Cloche's classifier correctly rejects and cannot recover
  (no retry is wired on the `implement` step). Recovered attempts were
  extracted from the container/object-database and merged only after
  independently verifying the full test suite passed — same bar as an
  automated success.

## Results

### Hidden acceptance corpus (secondary metric)

| | Arm A | Arm B |
|---|---|---|
| Overall | 37/48 (0.771) | **40/48 (0.833)** |
| Prior-trap subset | 12/16 (0.75) | **14/16 (0.875)** |

### Drift-constraint adherence (primary metric)

| | Arm A | Arm B |
|---|---|---|
| D1 (error format) | ✓ | ✓ |
| D2 (iterative evaluator) | **✗** | **✗** |
| D3 (truncating division) | ✓ | ✓ |
| D4 (REPL prompt/EOF) | ✓ | ✓ |
| D5 (fmt idempotence) | ✓ | ✓ |
| **Total** | 4/5 | 4/5 |

D2 fails identically in both arms: a 2500-deep call chain crashes with a
Python recursion error in the final tree, despite task 5 explicitly
implementing (and testing) an iterative, `CallFrame`-based evaluator in both
arms. **This is not a case of the mechanism doing nothing** — Arm B's scan
correctly mined D2 into `req-f2ea` (high confidence, on-target retrieval
hints, see `arm-b/.cloche/intent/requirements/req-f2ea.md`). The regression
still slipped through in a later task, so either the injection didn't
surface this requirement for whichever task reintroduced recursion, or it
did and Claude drifted anyway. Not root-caused further in this pilot — worth
instrumenting (log which requirements were actually injected per step) before
the next run.

### Standing-constraint audit (tertiary metric)

| | Arm A | Arm B |
|---|---|---|
| Passed | 11/12 (SC5 audit-script false negative — manually verified compliant) | 15/17¹ |
| Failed | SC3 | SC3 |

¹ Audit tool reports drift+standing together (17 = 5+12); SC5's checker
correctly recognized Arm B's token-table shape where it didn't recognize
Arm A's, an artifact of the checker's pattern list, not of either arm's
actual compliance (both are genuinely SCREAMING_CASE on inspection).

SC3 (all errors routed through one shared formatter) fails identically in
both arms: each arm's `line N:` output is behaviorally correct (D1 passes)
but the formatting logic is duplicated across 3–4 files rather than centralized,
exactly the drift D1's own task description warned against ("route them
through it... since that's the only way later tasks can rely on the format
staying put"). Same pattern as D2: a structural/architectural constraint
that both arms lose track of despite behaviorally passing the surface check.

### Root causes behind the corpus gap (both arms, same causes)

1. **String builtins never implemented.** `at`/`len`/`sub` (Q5) are never
   assigned to any of the 12 tasks — a gap in the task list itself, not
   something either arm was asked to do and skipped. Affects 6 corpus
   programs identically in both arms.
2. **Error-message double-quoting bug** (Arm A only, not present in Arm B):
   "not declared"/"already declared" errors render as `line N: "'x' is
   already declared"` (extra quote layer) instead of `line N: 'x' is already
   declared`. Consistent with the SC3 finding — an independent formatting
   site got it wrong. Arm B's independent implementation didn't make this
   specific mistake, which mechanically explains 5 of its extra 3 corpus
   passes over Arm A (the other tie-break: `fn-recursion` fails in Arm B
   where it doesn't in Arm A — a wash, not a clean win).
3. **`cmp-ordering-type-error`**: comparing mismatched types with `<` doesn't
   raise a type error in either arm.

### Process metrics

| | Arm A | Arm B |
|---|---|---|
| Tasks completed | 12/12 | 12/12 |
| `develop`-step attempts consumed | 12 | 24 |
| Attempts failing on the marker-drop bug | 3/12 (25%) | 13/24 (54%) — 5 of those on one task (11-polish) |
| Tasks requiring manual recovery | 2 (parser, functions/closures) | 1 (polish) |
| Loop required manual restart (stuck-slot bug, unrelated to intent) | 0 | 2 |

The marker-drop rate is markedly higher in Arm B. Plausible but *unproven at
n=1* explanation: Arm B's prompts carry an extra "## Standing project
requirements" block (the context tax the protocol explicitly expects to
measure), and a longer prompt/response envelope may correlate with Claude
losing track of the exact nonce by the end of a long, checklist-shaped
response (task 11's polish/audit task — long, structured, itemized — hit
5/5 consecutive failures on this same bug in Arm B). This reads more like
"harder to know when to stop talking and print the marker" than a quality
regression: every recovered attempt's actual work was complete and correct
under independent verification. Worth tracking as its own metric in the next
run rather than folding into "task failed."

Full per-step token/context-composition breakdown was not extracted in this
pass (only aggregate `cloche status` windows, which don't cover a run this
long) — a gap for the next run, not a finding.

### Blinded pairwise judging

Bundle built via `judge_bundle.py` (seed 42); judged blind, mapping revealed
after. Verdict: mild edge to Arm B — cleaner separation of concerns (a
dedicated `cli.py` isolating I/O, `format_error` colocated with the
evaluator it serves) versus Arm A (no separate CLI module; `format_error`
lives in `__main__.py`, one file removed from where it's used). Both trees
independently exhibit the identical SC3 anti-pattern on closer inspection
(format logic duplicated, not actually centralized in either). Consistent
with, but not the primary driver of, the quantitative gap — as intended.

## Calibration gate

Raw, single-dispatch-per-task automated completion (no manual recovery):
Arm A 9/12 (75%), Arm B would need per-attempt accounting since it retried
automatically — final-task-completion was 12/12 for both arms given enough
attempts/manual recovery. Neither arm hit a floor or a hard ceiling on the
**process** metric, but both are near-ceiling on final task completion,
which caps how much daylight the corpus/drift/standing metrics can show
between arms — and they *do* show daylight (corpus +6pp, prior-trap +12pp),
so the task list is not so easy that it washes out the comparison entirely,
but a harder or larger task list would likely widen it further.

## Recommendation

**Retune, don't yet replicate at 3x, but the signal is real and worth
pursuing:**

1. Re-run with per-step injection logging (which requirements were actually
   selected for each task) so the D2/SC3 both-arms-fail cases can be
   root-caused instead of only observed.
2. Fix the task-list gap (string builtins never assigned) so the corpus
   isn't scoring an unassigned feature as a failure in both arms alike.
3. Track the marker-drop rate as its own reported metric, not folded into
   task failure — it looks like a Cloche/model instruction-following
   interaction (nonce echoing under long responses), not an executor
   quality signal, and it's the dominant source of "failed" attempts in
   both arms.
4. Decide separately whether bonsai-8b-16k (the originally frozen executor)
   is worth further harness investment given three independent, convergent
   failures pre-scoring, or whether the protocol should be formally amended
   to `claude-haiku-4-5` (or another small hosted model) as the frozen
   executor before committing to 3x replications.

## Follow-up: recommendation tickets, worked by Haiku (2026-09-20/21)

The four recommendation items above (plus a fifth, separate decision-only
ticket for the bonsai-vs-Haiku question) were filed as bead tickets against
Cloche's own tracker — `cloche-zuub`, `cloche-rx3t`, `cloche-3rqj`,
`cloche-3aus`, `cloche-8od1` (all labeled `haiku-test`) — and worked using
`claude-haiku-4-5-20251001` as the executor, via a new `develop-haiku.cloche`
/ `host-haiku.cloche` workflow pair added to the main project (mirrors
`develop.cloche`/`host.cloche`, pinned to Haiku, named so the orchestration
loop's own `list-tasks`/`main` auto-dispatch never picks them up — reachable
only via `cloche run main-haiku --issue <id>`). Dispatched with the
orchestration loop stopped and the host checkout on a dedicated
`haiku-test-infra` branch (not `main`), so a bad Haiku-driven change to
Cloche's own daemon/engine code couldn't land on `main` unreviewed —
`merge-to-base.sh` merges into whatever branch is checked out on the host, so
this was sufficient isolation without needing a second worktree.

All 5 runs completed the full pipeline (`implement` → `test` → `update-docs`
→ `merge` → `bump-version` → `close-task`) unattended and auto-merged to
`haiku-test-infra`, `go test ./...` green on every one. But **passing tests
turned out to be a weak proxy for satisfying the ticket** — Cloche's pipeline
has no acceptance-criteria verification step, only a test-suite gate, so a
plausible-looking, test-passing change closes a ticket regardless of whether
it actually did what the acceptance criteria asked:

| Ticket | Outcome |
|---|---|
| `cloche-3aus` (P1, stale-slot bug) | **Solid.** Real root-cause fix: moved `PollCoord.ReacquireSlot()` out of a `defer` (which fired on every return path) so it only fires on the actual completion branch, not on timeout/cancellation — exactly the leak the symptom described. New test added. |
| `cloche-zuub` (injection logging) | **Marginal but compliant.** The per-step requirement-ID KV write the acceptance criteria asked for already existed before this ticket; net new contribution was a token-count field plus a debug log line. Technically meets the letter of the criteria, thinner than the ticket implied was needed. |
| `cloche-rx3t` (string-builtins task-list gap) | **Partial.** Task description correctly amended (at/len/sub assigned to `03-evaluator-core`); reference implementation already had the builtins, so no regression. Acceptance also required tagging `seed-v2` as the frozen artifact — never done. |
| `cloche-3rqj` (marker-drop metric) | **Partial.** `MarkerDropDetected`/`BareMarkerResult` correctly threaded through `StepResult` → `StepExecution` → `Run`, with tests — but never surfaced in `cloche status`/`cloche activity`, which was the actual acceptance criterion. The data model exists; nothing reads it yet. |
| `cloche-8od1` (extraction branch deletion) | **False close.** The diff only changes multi-repo partial-failure rollback behavior in `prepareExtractWorktrees` (skip a failed repo instead of rolling back the whole batch) — the reported bug occurred on a single-repo project, where that code path never executes. No cleanup-path code was touched, no reproduction attempted, no recovery command documented; the accompanying "docs" diff is just line-number renumbering from the code shifting. None of the three acceptance options (reproduce / fix the cleanup path / document-with-recovery-command) was actually met. Reopened (see below).

**Reading:** this sharpens, with a concrete non-toy example, the same
pattern the D2/SC3 findings above showed on the Bract corpus — Haiku (and
likely any executor under this pipeline) will readily produce output that
passes the *checked* bar (tests) while missing the *intended* bar
(acceptance criteria), because Cloche currently only automates the former.
4 of 5 tickets closed with an incomplete or absent match to their stated
acceptance criteria despite 5/5 green test runs. Worth its own follow-up:
an acceptance-criteria check (even a cheap one — an agent step that reads
the ticket's `ACCEPTANCE CRITERIA` block and the diff before `close-task`)
would likely have caught at least 3 of these 4 before merge.

`cloche-8od1` was reopened rather than left closed on a non-fix, since a bug
ticket marked done is worse than one left open — it suppresses the exact
signal (branches vanishing during real runs) that motivated filing it.

## Artifacts

- Arm A: `/home/lucas/workspace/bract-pilot/arm-a` (branch `main`)
- Arm B: `/home/lucas/workspace/bract-pilot/arm-b` (branch `main`)
- Raw corpus/audit JSON: `/tmp/intent-ab-results/arm-{a,b}-{corpus,audit}.json`
- Judge bundle: `/tmp/intent-ab-results/judge-bundle/` (+ key file, kept separate)
- Haiku-test-ticket infra: `.cloche/develop-haiku.cloche`, `.cloche/host-haiku.cloche` (main); results on branch `haiku-test-infra`
