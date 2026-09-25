# Intent A/B — Replications on seed-v2 (2026-09-22)

**Status:** complete — 3 replications per arm (x4), plus one control replication (x3).
Protocol: [`2026-09-14-intent-ab-experiment-protocol.md`](../../2026-09-14-intent-ab-experiment-protocol.md).
Round one (pilot, n=1): [`2026-09-17-intent-ab-pilot/`](../2026-09-17-intent-ab-pilot/README.md).

## What this round was for

Round one left two open questions and one known gap: was the D2 requirement (which
arm B's scan extracted correctly) ever injected into the tasks that regressed it; is
the arm-B edge real at n>1; and the task list never assigned the string builtins. The
plan for this round was: freeze seed-v2 (builtins assigned), run 3 replications per
arm under monitors that abort on infrastructure failures, and add an explicit audit
of what the extractor produces (the protocol's secondary experiment).

Executor in both arms: `claude-haiku-4-5-20251001` via Cloche's native adapter
(protocol amendment). Frozen artifacts: `seed-v2 = 1578d3de`; arm overlays and
driver `0ce2f0d3` (x4), `dca839e5` (x3, see below).

## What we found before the replications could be trusted

Three setup defects, each caught by a run and fixed before the next:

1. **Marker drop on task 1** (first launch, 3 minutes in): Haiku finished the lexer
   (72/72 tests) and printed the bare `CLOCHE_RESULT:success`; the nonce classifier
   rejected it and the task was reopened for retry — round one's dominant "failure".
   Fix (`dca839e5`, both arms): `implement:fail -> commit -> test`, so the test suite,
   not the agent's marker, gates the step; `fix-tests` bounded by `give-up`.
2. **Arm B never injected anything task-specific** (x3 replication 1). The daemon
   builds each step's retrieval query from `TaskDescription + step + workflow`, and reads
   `TaskDescription` from `.cloche/runs/<task-id>/prompt.txt` — a file only
   `cloche run --prompt` writes. Tracker-dispatched tasks queried on `"implement develop"`
   alone, so every task got the same four generic standing constraints
   (`req-a3f8 req-1b53 req-b38f req-ef2b`). `cloche intent preview` with and without the
   task text confirms it: the D2 requirement is selected for task 5 with the text, never
   without. **This was true in round one as well**, so both rounds compared "four generic
   rules" against "nothing". Fix (`0ce2f0d3`): the overlaid `prepare-prompt.py` also
   writes the task prompt to that path. Cloche's own `main` pipeline has the same gap
   (`cloche-hbcv`, P1). x3 replication 1 is kept as a *generic-injection control*.
3. **D2 was never satisfied**, at any evaluator commit in any arm. Task 5 asks for a
   deep-chain test of "hundreds of frames"; Python's recursion limit is 1000; the audit
   checks 2500. A naive recursive evaluator passes the task's own test and fails the
   audit, so D2 measures nothing about drift. Seed-v3 must set the task threshold above
   the recursion limit.

Two audit-checker defects were also fixed (`2cbe6c16`) and every arm re-scored: SC5
did not recognise a module-level constant table as a token-type set (arm A's shape, in
round one too), and SC7 flagged the interpreter's own `.eval()` method as Python's
builtin. Numbers below are from the corrected audit.

## Results

All arms: 12/12 tasks, one attempt per task, zero fix loops, zero marker drops,
zero infrastructure events. Wall time per arm 32–48 min.

### Hidden corpus (48 programs) and prior-trap subset (16)

| run | arm A | arm B |
|---|---|---|
| round one (seed-v1, generic injection, manual recovery) | 37/48 · 12/16 | 40/48 · 14/16 |
| x3 r1 — control, generic injection | 48/48 · 16/16 | 47/48 · 16/16 |
| x4 r1 — task-specific injection | 47/48 · 16/16 | 46/48 · 16/16 |
| x4 r2 | 48/48 · 16/16 | 48/48 · 16/16 |
| x4 r3 | 48/48 · 16/16 | 48/48 · 16/16 |

Medians across x4: arm A 48/48, arm B 48/48. The only recurring corpus failure is `cmp-ordering-type-error` (comparing mismatched
types with `<` doesn't raise), plus one `cf-nested-blocks-scope` in x4 arm B r1.
Haiku on seed-v2 is at ceiling; the corpus cannot separate the arms.

### Constraint audit (D1–D5 + SC1–SC12, corrected checkers)

| run | arm A | arm B |
|---|---|---|
| x3 r1 (control) | 15/17 — D2, SC3 | 15/17 — D2, SC3 |
| x4 r1 | 16/17 — D2 | 16/17 — D2 |
| x4 r2 | 16/17 — D2 | 15/17 — D2, SC3 |
| x4 r3 | 14/17 — D2, D4, SC3 | 15/17 — D2, SC4 |

D2 fails everywhere for the reason above. Excluding it, across the three x4
replications arm A lost one **drift correction** (D4 in r3: the REPL never printed the
`bract> ` prompt) and one standing constraint (SC3 in r3); arm B lost no drift
corrections and two standing constraints (SC3 in r2 — `line N:` formatting duplicated
across four files; SC4 in r3 — a `sys.stderr.write` in `errors.py`). The drift
corrections are the mid-project asides the memory is meant to carry forward, and the
one loss is in the arm without it — the direction the hypothesis predicts, at n=1
event. The standing constraints are in DESIGN.md from the start and were lost at the
same rate in both arms.

### Extractor audit (arm B only; `eval/audit/intent_audit.py`, blind Sonnet judge)

| scan | reqs | seeded recall /24 | unseeded valid/total | dev-incident | dups | noise |
|---|---|---|---|---|---|---|
| round one arm B | 29 | 18 | 8/11 | 2 | 0 | 3 |
| x3 arm B r1 | 35 | 24 | 9/9 | 1 | 3 | 1 |
| x4 arm B r1 | 32 | 24 | 6/6 | 1 | 0 | 0 |
| x4 arm B r2 | 29 | 23 | 3/7 | 0 | 0 | 4 |
| x4 arm B r3 | 27 | 23 | 7/7 | 1 | 0 | 0 |

Across the three x4 scans: seeded recall 24, 23, 23 of 24 (the miss is SC11, 1-indexed
line numbers, twice); one dev-incident rule per scan (`req-a2c2` "wrap every CLI entry
point, report `line 0: internal error`" in r1; "the REPL survives errors" in r3).

Round one missed five of the seven anti-prior traps (Q2–Q6) and D3, and its noise was
about the experiment harness (the bonsai wrapper source lived inside the arm tree). The
scanner has had fixes land since and the arms no longer carry harness code: recall is
24/24 in two of three fresh scans. The one clean dev-incident extraction in x4 r1 is
`req-a2c2` ("every CLI entry point wraps its body in a top-level try/except and reports
`line 0: internal error: …`"), mined from a commit, not the spec — the behaviour the
secondary experiment was looking for. x4 r2's four noise items are all mined from *this
round's overlay comments* (executor choice, the NodeSource Dockerfile note, the
`prompt.txt` path, the `implement:fail` wiring), which sit in the arm's overlay commit.
The extractor is faithful to whatever is in the history; seed-v3 must squash the
overlay into the seed commit and keep overlay files comment-free.

### Process

| arm | attempts | wall | tokens (status scrape, best-effort) |
|---|---|---|---|
| x3 A r1 / B r1 | 12 / 12 | 39m / 41m | 92k / 109k |
| x4 A r1 / B r1 | 12 / 12 | 32m / 39m | 117k / 54k |
| x4 A r2 / B r2 | 12 / 12 | 43m / 38m | 181k / 121k |
| x4 A r3 / B r3 | 12 / 12 | 35m / 48m | 130k / 45k |

Zero marker drops in 72 attempts (96 including the control), versus 25–54% of attempts in round one. The wiring
change removed the *consequence* of a drop, not the drop itself, and the monitor still
records drops as events — none occurred. Round one's drops concentrated on long,
checklist-shaped tasks under manual-recovery pressure; this round's runs were fast and
unattended. Not root-caused.

## Validation of the mechanism (2026-09-23)

Before treating the above as an arm comparison, the intent-continuity chain was
checked end to end, in order: is each failed requirement present in arm B's store; was it
selected for the task that needed it; did it land in that task's prompt.

1. **Presence.** SC3, SC4, D2 and D4 are present and clearly stated in all four arm-B
   stores. `cmp-ordering-type-error` and `cf-nested-blocks-scope` have no requirement
   in any store — and no explicit source: DESIGN.md only implies that ordering
   operators are Integer-only (§1) and that block bodies get a fresh scope (§4), and no
   task prompt states either. Seed under-specification, not extraction; arm A misses
   them at the same rate.
2. **Selection.** Reconstructed from each arm's store as of the task's dispatch (the scan
   commits are in git) with `cloche intent preview` and the task text: in x4 arm B r2
   the SC3 requirement was selected for tasks 4 and 7 and SC4's for tasks 7 and 11.
3. **Injection.** The daemon logs `resolving intent injection for step "implement":
   intent: parsing domains.yaml: … mapping values are not allowed` — without the project
   path — on every post-scan implement step of **x3 arm B r1 and x4 arm B r3**. Their
   first scan wrote a `domains.yaml` whose description contained an unquoted `: `;
   `intent.Resolve` fails closed on that and nothing is injected. Ten of twelve tasks in
   each of those runs ran with no memory. x4 arm B r1 and r2 logged no such error.
   Filed `cloche-etr2` (P1): the scan agent hand-writes the file, `Resolve` should
   degrade rather than disable, the log line should name the project.
4. **Ground truth.** A copy of x4 arm B r3 with the implement prompt replaced by "echo
   the prompt you received verbatim", run through the real pipeline on task 7: under the
   broken store the prompt contained no requirements block; after quoting the YAML it
   contained the block with exactly the nine IDs `preview` predicted, SC3's, SC4's and
   D2's among them.

**Corrected reading.** Only x4 arm B r1 and r2 are arm-B replications. Against arm A
(x3 r1, x4 r1–r3): corpus B 46, 48 vs A 48, 47, 48, 48; audit losses (D2 excluded) B r1
none, B r2 SC3; A r1 none, r2 none, r3 D4 + SC3. In B r2 the SC3 requirement was selected
for tasks 4 and 7 and — the mechanism now verified — injected, and the final tree still
duplicated the `line N:` formatting across four files: one real "injected but not
followed" event, against one "not remembered" event (D4) in arm A. That is the entire
between-arm evidence at n=2 vs n=4, and it is a tie.

**Consequence for the plan.** Harder tasks are not yet justified. In order: fix
`cloche-etr2` so the store cannot silently die; the driver now aborts an arm whose store
is unresolvable (`infra:intent_store_invalid`); make the two under-specified rules
explicit in the seed; then re-run seed-v2 at 3× to get a clean injected baseline. Only
if arms still fail identically with injection verified does the seed-v3 task list
(`docs/plans/2026-09-22-intent-ab-seed-v3-design.md`) go ahead.

## x5 — seed-v2.1 at 3×, injection verified per step (2026-09-24, in progress)

Clean run on seed-v2.1 (`d34b8662`: ordering comparisons, fresh block scope and the D2
depth made explicit) with the CSV/check/repair intent-scan (`681b0b8c`, `f701eb19`),
the `task_prompt_path` retrieval fix (`cloche-hbcv`) and the per-step injection record
(`intent: injected N requirement(s) into project … task … step "implement": <ids>`) in
the daemon log. Monitors armed; no `ERROR intent` line and no infra abort so far.

### Replication 1

| | corpus | traps | audit (17) | lost |
|---|---|---|---|---|
| arm A r1 | 48/48 | 16/16 | 16 | SC3 (`line N:` formatting duplicated across evaluator/lexer/parser) |
| arm B r1 | 48/48 | 16/16 | 15 | D2 (reference fails it too — excluded), **D4** |

Both arms: 12/12 tasks, one attempt each, 12/12 merges, no fix loops. Score files:
`results/x5-arm-{a,b}-r1-scores.json`. Arm B ran two scan repair loops live (domains.csv
unquoted comma, 18 s; reconcile.csv with 13 columns, 26 s) — the pattern works.

**Injection is verified for every implement step.** Ten arm-B implement steps after the
first scan each logged 10–12 requirement IDs (store: 45 requirements, 9 project-level,
36 domain-level; `token_budget = 1000`). Seven project-level requirements
(`req-2e78, c15b, 9cf5, b44e, 656f, 9af0, 26a9`) appear in **every** injection; the
remaining 3–5 slots go to domain-scoped ones.

**The D4 loss is not a memory failure.** Reconstructed from the injection record plus a
per-commit D4 check:

- The D4 requirement (`req-a234`, "The REPL prompt string is exactly `bract> ` … EOF must
  end the session cleanly with exit code 0", provenance `prompt ju6e-main`) was created
  by the scan *after* task 8 at 18:53:44 — so it could not have been injected into task 8,
  whose own prompt carried the correction verbatim.
- Task 8's commit (`arm-b-r1-51c`) already fails D4, and every later commit does too.
  Arm B's REPL branches on `isatty`: on a tty it uses `input("bract> ")`; on a pipe it
  reads a line first and writes the prompt to **stderr** afterwards. The audit drives it
  with an empty pipe and looks in stdout, so it sees no prompt at all. Arm A writes the
  prompt to stdout unconditionally before each read. Neither DESIGN.md §9 nor the task
  says which stream; both say "read a statement, evaluate…". So this is a task-8
  implementation choice that the check's method disagrees with, in the same class as arm
  A's x4 r3 D4 loss (there, arm A simply forgot the correction).
- `req-a234` was selected for **none** of tasks 9–12 (`arm-b-r1-bep/n8j/5xx/04o`):
  under the 1000-token budget the seven project-level requirements take most of the
  slots and the domain-scoped `cli` requirement never ranks in. Those tasks (file runner,
  fmt, …) had no reason to touch the REPL prompt, so a re-selection would probably not
  have repaired it either — but this is the first verified case of *present, never
  selected* and it argues for a larger budget or a project-level cap before the next
  round, not for harder tasks.

### Replication 2

| | corpus | traps | audit | lost |
|---|---|---|---|---|
| arm A r2 | 48/48 | 16/16 | 17/18 | SC3 (`line N:` duplicated across `cli.py`, `errors.py`) |
| arm B r2 | 48/48 | 16/16 | **18/18** | — |

Both arms 12/12 first-attempt, 12/12 merges. Arm B's scans ran five repair loops
(domains ×2, candidates ×1, reconcile ×2 — every one an unquoted comma in a free-text
field), all fixed on the first retry; 0 give-ups, 0 `ERROR intent`. Store at the end:
32 requirements, only 2 project-level (`req-624b` "fix the implementation, never the
tests", `req-f276` test command), so each injection carried 5–7 requirements with 3–5
domain-scoped slots.

Selection, from the injection record: the SC3 requirement (`req-0138`, "every error …
`line N: <message>` … sourced from one place") was injected into tasks 7–12 including
task 11 (polish / standing-constraint audit), and the final tree passes SC3 — while arm
A lost SC3 in both r1 and r2. The D2 requirement (`req-0a08`, no Python call stack) was
injected into task 7 only; the D4 requirement (`req-7ff3`) into none of 9–12 (as in r1);
both passed on the strength of the task's own implementation. Traps Q1–Q6 were injected
into tasks 3–6 as expected.

### Extractor audit, x5 r1–r2 (blind Sonnet judge; `results/x5-arm-b-r*-intent-audit.json`)

| | graded | seeded recall | unseeded | valid | actionable | from incidents | noise | dup |
|---|---|---|---|---|---|---|---|---|
| r1 | 44 | 24/24 | 18 | 18 | 13 | 7 | 0 | 0 |
| r2 | 32 | 24/24 | 11 | 11 | 10 | 1 | 1 | 0 |

Recall of every planted constraint again 24/24 in both. The r1 incident-derived
requirements are the ones the experiment wants to see — corrections learned during
development, not restatements of the spec: "REPL keeps reading after an error",
"binding collision across loop iterations", "`sub` with start > end", "no statement of a
failed program may have begun executing", "closure scope loss", "don't add bracket
parsing". r2's single noise item is the "working name Sprout was rejected" trivia
(`req-3de8`, domain-scoped, never injected into an implement step). The judge's own CSV
needed the repair call once (r2: line 54, 11 columns — the same unquoted-comma slip the
scan agents make).

### Replication 3

| | corpus | traps | audit | lost |
|---|---|---|---|---|
| arm A r3 | **45/48** | 16/16 | 16/18 | D2, SC3 (`line N:` duplicated across `cli.py`, `evaluator.py`, `lexer.py`, `parser.py`) |
| arm B r3 | 48/48 | 16/16 | 17/18 | D2 |

Arm A's three corpus misses: `str-at-non-string-error` (message printed as `at expects a
string`, no quotes) and `fn-recursion` + `cf-if-else-chain`, where a `return` inside an
`if` body evaluated through the task-5 "thunk" path escapes as a raw Python
`ReturnValue` exception with a traceback — its iterative-evaluator attempt broke
`return`-in-block and nothing later caught it. Arm B r3: three scan repairs (all
recovered), 0 `ERROR intent`; store 35 requirements, 7 project-level. Its D2 requirement
(`req-da4e`, "the evaluator must be iterative", created after task 5) was injected into
tasks 6–8 and the tree still recurses — the same "injected, not acted on" shape as x4 B
r2's SC3, on a check the reference implementation fails too. SC3's requirement
(`req-b696`) was injected into tasks 3, 7, 8, 10, 12 and SC3 passes.

Extractor audit r3: 35 graded, seeded recall **21/24** (Q1 Boolean-condition, SC7, SC11
not extracted — the first recall misses in five audited stores), 13 unseeded, 11 valid,
10 actionable, 3 incident-derived, 2 noise.

### x5 totals (three replications each, injection verified on every arm-B implement step)

| | corpus (144) | traps (48) | audit losses, D2 excluded |
|---|---|---|---|
| arm A | 141 (48, 48, 45) | 48 | SC3, SC3, SC3 |
| arm B | **144** (48, 48, 48) | 48 | D4 (task-8 stream choice, see r1) |

Process was identical in both arms: 12/12 tasks, one attempt each, 12/12 merges, no fix
loops, in every replication; arm B took 3–4 min longer per run for its scans (10 scan
repair loops across the three runs, 0 give-ups). No infra abort fired.

The between-arm difference in x5 is entirely SC3 — "`line N:` formatting must come from
one place" — which arm A lost in all three runs and arm B in none, with the SC3
requirement injected into the polish task (11) in r2 and into 5–6 tasks per run in all
three. That is the effect the experiment was designed to see: a standing constraint that
the spec states once, that a weak executor drops when its context no longer contains it,
and that injection keeps in front of it. It is one constraint, three of three, at n=3 —
suggestive, not conclusive. Arm A's r3 corpus loss (45/48) has no arm-B counterpart in
any of the seven clean arm-B runs (x4 r1–r2, x5 r1–r3: 46, 48, 48, 48, 48).

## Reading (updated after x5, 2026-09-24)

- **The mechanism is validated end to end.** Every failed requirement that the seed
  states is present in the store (recall 24/24 in four of five audited stores, 21/24 in
  the fifth); the ones that were absent were absent from the seed and are now explicit;
  selection is reconstructible from the per-step injection record; injection is proven
  by the echo diagnostic and by that record on every one of the 30 arm-B implement steps
  in x5, with zero `ERROR intent` lines. This was Lucas's precondition for reading the
  arms against each other at all.
- **The primary question now has a first, small, positive signal.** x4 (n=2 clean B vs
  n=4 A) was a tie. x5 (n=3 vs n=3, injection verified) is not: arm A lost SC3 in every
  run and one corpus program set (45/48) in r3; arm B lost nothing but the excluded D2
  and one task-8 stream choice (D4, r1). SC3 is exactly the kind of rule intent
  continuity exists for — stated once in the spec, invisible in a later task's context,
  and cheap to violate by adding one more `f"line {n}: …"` — and the SC3 requirement was
  in arm B's prompt for 5–6 of 12 tasks in every run. Three of three on one constraint
  at n=3 is suggestive; it is not a result. Haiku is otherwise still at the corpus
  ceiling (285/288 across x5), so the experiment's remaining headroom is in the audit
  and in longer histories, not in this corpus.
- **The secondary question has a clear answer.** The extractor recalls the planted
  constraints (24/24 ×4, 21/24 ×1), produces mostly valid unseeded requirements (x5: 42
  unseeded, 40 valid, 33 actionable, 3 noise, 0 duplicates), and extracts real rules from
  incidents — 11 of them across x5, e.g. "REPL keeps reading after an error", "closure
  scope loss", "no statement of a failed program may have begun executing". The noise
  it does produce is history trivia ("the name Sprout was rejected"), domain-scoped and
  never selected for an implement step.
- **Two mechanism findings for the next round.** (1) With `token_budget = 1000` a store
  with 7–9 project-level requirements spends most of each injection on them (r1: 7 of
  ~11 slots) and a domain-scoped requirement can go unselected for the rest of the run
  (`req-a234`, r1). (2) "Injected, not acted on" is real and repeatable when the
  requirement asks for a rewrite the current task has no reason to make (D2 in x5 B r3,
  SC3 in x4 B r2): injection keeps a rule visible, it does not schedule the work.
- **The scan's CSV/check/repair loop earns its keep**: 10 repair loops in x5 (every one
  an unquoted comma in a free-text field, from Sonnet), 10 recoveries on the first
  retry, 0 give-ups, versus the pre-fix behaviour where the same slip in YAML silently
  disabled injection for the rest of the run.

## Next

1. **Replicate the SC3 signal before building on it**: another 3× on seed-v2.1 as-is
   (cheap: ~45 min per arm-pair), pre-registering "arm A loses SC3, arm B does not" as
   the hypothesis. If it holds at 6/6 vs 0/6 it is worth reporting as an effect.
2. **Then widen the headroom** with the seed-v3 design (on hold): longer task list with
   more cross-cutting standing constraints of the SC3 shape; fix the reference
   evaluator so D2 becomes a live check; keep the string-builtins assignment.
3. **Budget/selection**: raise `token_budget` or cap project-level requirements per
   injection so domain-scoped ones are not crowded out; measured from the injection
   record, not guessed.
4. **Scan prompts**: add a quoted example row to `discover-domains.md`, `extract.md`,
   `reconcile.md` and re-measure the repair rate (10/~36 scan steps in x5).
5. **Cloche:** `cloche-hbcv` and `cloche-etr2` are fixed on main and `make install`ed;
   the auto-scan-after-`main` path in Cloche's own pipeline still needs
   `task_prompt_path` set by its `prepare-prompt` to get task-specific injection.

## x6 — pre-registered replication of the SC3 result (2026-09-25)

**Hypothesis, stated before launch:** on seed-v2.1 with the x5 harness, arm A (no
memory) fails SC3 in each replication and arm B (memory) does not. Prediction under the
null: SC3 losses are independent of arm at roughly arm A's x4+x5 rate (4 of 6 runs), so
3 arm-B runs would be expected to lose it about twice. Secondary predictions: corpus and
traps at ceiling in both arms (no separation expected); D2 excluded (reference fails it);
any D4 loss is inspected for the task-8 stream choice before being counted.

**Isolation fixes applied before launch** (found while auditing what else could instruct
Haiku inside the container; both arms were affected identically in x5, so x5 stands, but
they are removed for x6):

- The host's `~/.claude/settings.json` was copied whole into every container. It carried
  a `SessionStart` hook (a host-path script — it ran and failed in every x5 session),
  `model`, `effortLevel`, `alwaysThinkingEnabled` and permissions. Autonomous containers
  now receive only the auth/env keys; interactive consoles are unchanged
  (`internal/adapters/docker/authfiles.go`).
- The exact prompt each agent step received is now written to
  `.cloche/logs/<task>/<attempt>/<workflow>/<step>.prompt.md` beside its transcript, so
  the presence/absence of the standing block in every arm-A and arm-B step is a file
  read (`grep -c "Standing project requirements"`), not an inference from the daemon log.
- Claude Code in the arm image is pinned to `2.1.281` (x5's version); an unpinned
  `npm install -g` would have moved x6 to whatever shipped since.

Everything else — seed-v2.1 `d34b8662`, task list, overlays, `token_budget = 1000`,
executor `claude-haiku-4-5-20251001`, scan model Sonnet, monitors — is as in x5.

**Smoke test on the rebuilt daemon (3.24.24), echo diagnostic project, one real
`main` run:** image rebuilt from the pinned Dockerfile; daemon logged
`settings.json … reduced to auth keys [] (autonomous run)`; the container authenticated
and ran Claude Code 2.1.281 as `claude-haiku-4-5-20251001`; the stream contains no
`hook_started`; `develop/implement.prompt.md` was written (4.4 KB) with the
`## Standing project requirements` block (8 IDs) above the task text. The run then
"failed" for the right reason: the echo store had mined a requirement from the earlier
echo diagnostic itself — `req-bae5`, "do not have an agent dump the full raw prompt into
a committed file, even for a diagnostic" — and Haiku refused the echo task, citing it.
An injected incident-derived requirement overriding a task prompt is the mechanism
working end to end; the diagnostic prompt is simply no longer usable in that project.

**Isolation verified on x6's own steps** (`results/x6-*`): task-1 prompts in the two arms
are byte-identical except for the per-step nonce (2,640 bytes each, no block — the store
does not exist yet); task 2 likewise (the post-task-1 scan, 12:55–12:58, was still
running when task 2 was dispatched, so injection starts at task 3 by construction, in
every arm-B run of x5 and x6); task 3 onward, arm B's prompt = arm A's prompt + the
standing block, with the recorded IDs equal to the daemon's injection line. No
`hook_started` in any stream; `settings.json` reduced to `[]` auth keys for every
container; Claude Code 2.1.281; executor `claude-haiku-4-5-20251001`. One environmental
difference from x5, identical in both arms: the daemon's extraction change of the morning
(`3f8664bd`) preserves the in-container commit message, so merge commits now read
`implement task changes` — text the arm-B scan mines.

### Replication 1 — the pre-registered prediction fails immediately

| | corpus | traps | audit | lost |
|---|---|---|---|---|
| arm A r1 | 48/48 | 16/16 | **18/18** | — (first arm-A run to pass D2) |
| arm B r1 | 48/48 | 16/16 | 16/18 | SC4 (`errors.py:24 sys.stderr.write`), SC12 (stub `pass` in `evaluator.py`) |

Arm A kept SC3. The x5 streak (3/3) was a small-n streak, not an arm-A property. Arm B's
two losses, traced with the per-step prompt record (`.cloche/logs/<task>/<attempt>/develop/implement.prompt.md`):

- Both violations entered at task 7 (errors; merge `a10a3d7`, 20:19 UTC). Task 7's prompt
  carried `req-517b` ("every error rendered via one shared error-formatting path, not ad
  hoc print/raise call sites" — the first scan's reading of §10) but not the exact SC4
  rule; `req-b117` ("no module other than `bract/__main__.py` may call print /
  sys.stdout.write / sys.stderr.write") was only created at 20:33, from task 11's own
  prompt text, after task 11 had run. Same for SC12: `req-f6bd` (no stub bodies) was
  created at 20:33 from the same prompt. So the polish task (11) had both rules in its
  *task text* and still left both violations — that is Haiku, not memory — and no
  earlier task ever had the precise rules to follow. An extraction-precision gap on §10
  (the first scan produced the looser `517b`), not an injection gap.
- Ten of twelve steps injected (5–8 requirements each; 36 in the store at the end, 5
  project-level).

Extractor audit r1: 35 graded, seeded recall 23/24 (Q3 missed), 11 unseeded, 11 valid, 10
actionable, 4 incident-derived, 1 noise, **4 near-duplicates** flagged (the reconcile
step created rather than merged: `05a5`/`3eac`→`2f18`, `0e7d`→`d6c4`, `115c`→`0ae3`) —
the first store with duplicates.

### Replication 2 — arm B loses SC3 with the rule in its prompt

| | corpus | traps | audit | lost |
|---|---|---|---|---|
| arm A r2 | 48/48 | 16/16 | **18/18** | — |
| arm B r2 | 48/48 | 16/16 | 17/18 | **SC3** (`cli.py`, `evaluator.py`, `lexer.py`, `parser.py`) |

Running the SC3 checker at every merge commit of both arms settles what SC3 measures:

```
task:      1     2     3     4     5     6     7     8     9    10    11
arm A r2  PASS  FAIL  FAIL  FAIL  FAIL  FAIL  PASS  PASS  PASS  PASS  PASS
arm B r2  PASS  FAIL  FAIL  FAIL  FAIL  FAIL  FAIL  FAIL  FAIL  FAIL  FAIL
```

The parser task (2) *always* adds a second `line N:` formatter beside the lexer's — in
every run of either arm — and the tree passes SC3 at the end only if the errors task (7)
centralises the formatting. SC3 is therefore not "did the model remember a rule across
ten tasks"; it is "did task 7 do the consolidation its own brief asks for", a per-run
coin flip for Haiku. Arm B r2's task 7 had the SC3 requirement (`req-8b3c`, created by
the first scan at 20:42, injected into tasks 4, 7 and 10) in its prompt and did not
consolidate; task 11 (polish) did not receive it (7 of ~7 slots went elsewhere) and did
not fix it either. This is the cleanest "injected, not followed" event so far — and arm
A r2's task 7 did the consolidation with no memory at all.

Extractor audit r2: 24 graded, seeded recall 23/24 (D2 missed), 6 unseeded, 6 valid, 6
actionable, 1 incident-derived, 0 noise, 0 duplicates. Store 26 requirements; 10 of 12
steps injected (4–7 requirements each).

## Artifacts

- Runs: `/home/lucas/workspace/bract-pilot/x4/` (report.json, results/*-scores.json,
  results/*-audit-v2.json, results/arm-b-r*-intent/), `x3/` (replication 1 = control).
- Extractor audits (judged): `x4/results/arm-b-r{1,2,3}-intent-audit.json`,
  `x3/results/arm-b-r1-intent-audit.json`, `arm-b/results/intent-audit.json` (round one).
- Driver: `experiments/intent-ab/harness/arm_driver/` (`replicate.py`, `monitors.py`);
  extractor audit: `experiments/intent-ab/eval/audit/intent_audit.py`.
