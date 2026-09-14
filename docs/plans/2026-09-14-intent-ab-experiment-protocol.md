# Intent Tracking A/B Experiment Protocol

**Date:** 2026-09-14
**Status:** Proposed (runs after the built-in migration, `cloche-la93`…`cloche-cg8a`, lands)
**Prior:** [`2026-09-13-intent-continuity-design.md`](2026-09-13-intent-continuity-design.md),
[`2026-09-14-intent-builtin-migration-design.md`](2026-09-14-intent-builtin-migration-design.md)

## Question

Does intent tracking measurably improve what a **weak executor model** produces
when Cloche supplies the development structure? The retrieval spike measured
whether the right requirements reach the prompt; this experiment measures whether
that changes the software that comes out the other end.

The interesting case is deliberately hostile: the executor is a small local model
(**bonsai-8b-16k**, 1-bit, 16k context) that forgets standing constraints readily
and cannot afford wasted context. If injection pays for its ~1–2k-token context
tax *here*, the mechanism is doing real work.

## Design

Two arms, identical in every respect except intent tracking. Same seed repo, same
task list verbatim, same workflow DSL, same executor model and sampling settings,
same budgets.

| | Arm A — vanilla | Arm B — intent |
|---|---|---|
| `.cloche/intent/` | absent | bootstrapped by first scan |
| `intent.scan_after_tasks` | `false` | `true` (default) |
| Injection | dormant (no intent dir) | automatic |
| Extractor / hint writer | — | production shape: Claude-class agent |
| Executor (all coding steps) | bonsai-8b-16k | bonsai-8b-16k |

Extraction uses the shipped configuration (capable scanner, weak executor)
because the claim under test is *does the feature as shipped help* — not whether
a weak model can also write its own hints. An all-local extraction sub-arm is a
possible follow-up, not part of this run.

**Replication:** pilot of 1 run per arm first, to shake out the harness and
calibrate task difficulty. Proceed to replications (target 3/arm) only if the
pilot passes its calibration gate (below).

## Subject: the Sprout language

A tiny interpreted language — small enough to finish, tricky enough to punish
forgetting. Implementation language **Python** (the executor's strongest), stdlib
only.

Spec highlights (full spec ships in the seed repo's `DESIGN.md`):

- Integers, strings, booleans; `let` bindings; `fn` definitions with closures;
  `if`/`else`; `while`; comparison and arithmetic operators.
- A REPL (`sprout`) and a file runner (`sprout run FILE`).
- Line-oriented error reporting; a `sprout fmt` canonical formatter.

~12 bead tasks in dependency order: lexer → parser → evaluator core → bindings →
functions/closures → control flow → errors → REPL → file runner → formatter →
polish/docs. The task list is authored once and copied verbatim into both arms.

## The measurements

### Primary: drift-constraint adherence

The signature capability intent tracking claims is carrying **mid-project
corrections** forward — exactly what a stateless agent loses. Five *drift
constraints* are injected into specific task prompts partway through the
project, phrased as offhand corrections, each one relevant only to **later**
tasks:

| # | Introduced in | The correction (illustrative) | Checked in |
|---|---|---|---|
| D1 | task 4 | "error messages must read `line N: message`, nothing else" | tasks 7–12 |
| D2 | task 5 | "the evaluator must be iterative — no recursion on user input depth" | tasks 6–12 |
| D3 | task 6 | "integer division truncates toward zero, never floor" | tasks 7–12 |
| D4 | task 8 | "the REPL prompt is `sprout> ` with a trailing space, and Ctrl-D exits cleanly" | tasks 9–12 |
| D5 | task 9 | "`sprout fmt` output must be byte-stable (fmt∘fmt = fmt)" | tasks 10–12 |

In arm B these live only in a past task's prompt/transcript until the post-task
scan mines them into requirements; in arm A they exist only in history no later
step can see. **Metric:** for each later task touching the constraint's surface,
does the final merged code comply? Scored by automated checks (regex/AST/behavior
tests per constraint), reported as adherence rate per arm. Exact wording and
placement are frozen in the seed repo before any run.

### Secondary: hidden acceptance suite

A corpus of **~40 Sprout programs with expected stdout/stderr/exit codes**,
written against the spec before any run and kept **outside both repos** (the
agents never see it). A harness script runs the corpus against each arm's final
`main`; metric is pass rate. Sub-scored by feature area so partial builds
compare fairly.

### Tertiary: standing-constraint audit

`DESIGN.md` seeds ~12 checkable standing constraints from day one (stdlib only;
single-package layout; `line N:` errors; no prints outside the CLI layer; token
names in SCREAMING_CASE; etc.). Post-hoc audit script counts violations in each
final tree. Arm B's scan should turn these into requirements on the first pass;
arm A relies on the model re-reading `DESIGN.md` unprompted.

### Process metrics (from cloche's own stores)

Per arm: tasks completed / attempted; attempts per completed task; fix-loop
iterations; output tokens consumed; wall time; merge failures. Plus **context
composition**: for arm B, the injected block's token share per step (the tax
being paid).

### Qualitative: blinded pairwise judging

A strong model judges the two final repos on code coherence and consistency,
**blinded**: before judging, strip `.cloche/` entirely from both copies, strip
git history, and randomize A/B labels. The judge score is color, not the
headline — the automated metrics are the headline.

## Harness

- **Agent adapter:** bonsai runs behind an OpenAI-compatible local endpoint
  (Ollama). The executor is driven by a scriptable coding-agent CLI pointed at
  that endpoint (candidate: `aider --model ollama/bonsai-8b-16k` in
  non-interactive mode), wrapped as the workflow's `agent_command`. Bonsai
  handling rules from the spike apply: **never temperature 0** (infinite
  reasoning loop → empty output), cap generation length, strip any leaked
  chain-of-thought before applying edits. The wrapper is part of the seed repo
  and identical in both arms.
- **Networking:** the container reaches host Ollama via the network allowlist +
  host gateway. The Claude-class scan agent in arm B runs host-side (intent-scan
  is a host workflow) exactly as in production.
- **Budgets:** per-task `max_attempts = 3`; per-task and per-project
  `token-limit` caps identical across arms; a wall-clock cap per run. Report
  both quality-at-budget and budget-spent.
- **`intent.token_budget`** in arm B: 1000 (tuned down for the 16k window; the
  chosen value is frozen pre-run and reported).

## Procedure

1. **Freeze artifacts** (before any run): Sprout spec + `DESIGN.md` with
   standing constraints; the 12-task list with drift constraints D1–D5 embedded;
   the hidden test corpus + harness; audit scripts; the agent wrapper. Tag the
   seed repo.
2. **Pilot:** one run per arm, loop-driven to task-list exhaustion or budget cap.
3. **Calibration gate** (arm-agnostic, checked on the pilot): the harness ran
   unattended; task completion landed between ~30% and ~90% in at least one arm
   (a floor-or-ceiling result means the task list needs retuning, not more
   replications); no metric was unmeasurable. Retune and re-pilot if failed.
4. **Replications:** 3 per arm (fresh clones, fresh containers, same frozen
   artifacts), if the pilot gate passes and the pilot delta warrants it.
5. **Evaluate:** run the hidden suite, drift and standing audits, process
   metrics, blinded judging. Medians across replications.
6. **Report:** results doc in `docs/plans/spikes/`, plus a published write-up.

## Confounds & controls

- **Contamination:** arm A must contain no `.cloche/intent/` at any point
  (assert in harness); the hidden corpus never enters either repo; judging is
  blinded and `.cloche/`-stripped.
- **Variance:** small-model runs are noisy — hence pilot-then-replicate, medians,
  and reporting per-replication spreads rather than single numbers.
- **Context tax:** arm B pays tokens for injection that arm A spends on code
  context. This is not a confound to remove — it *is* the trade under test — but
  it must be reported (context-composition metric).
- **Scan cost asymmetry:** arm B consumes Claude tokens at scan time. Reported
  separately from executor tokens; the hypothesis is about executor output
  quality, not total-cost parity.
- **Pre-registration:** metrics, drift wording, and gate criteria are frozen in
  this doc + the tagged seed repo before the first run. Post-hoc metric shopping
  is the failure mode this section exists to prevent.

## What would falsify the hypothesis

Arm B showing no improvement in drift adherence (primary), or buying adherence
at a hidden-suite cost exceeding it (context tax outweighing memory), across
replications. Either is a publishable result — it would say the feature needs
retuning for small-context executors (e.g., tighter budgets, fewer injected
requirements), which feeds directly back into `intent.token_budget` defaults.
