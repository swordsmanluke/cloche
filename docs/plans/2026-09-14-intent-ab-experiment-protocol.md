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

## Subject: the Bract language

A tiny interpreted language — small enough to finish, tricky enough to punish
forgetting. Implementation language **Python** (the executor's strongest), stdlib
only.

**Naming note:** the working name "Sprout" was checked and rejected — at least
four existing languages use it, including a Python tree-walking interpreter on
PyPI (our exact shape). "Bract" has no language collision (checked 2026-09-14;
nearest hits are a Clojure config framework and the unrelated Bracmat). A
name-collision check is part of the freeze procedure for any future subject.

Spec highlights (full spec ships in the seed repo's `DESIGN.md`):

- Integers, strings, booleans; `let` bindings; `fn` definitions with closures;
  `if`/`else`; `while`; comparison and arithmetic operators.
- A REPL (`bract`) and a file runner (`bract run FILE`).
- Line-oriented error reporting; a `bract fmt` canonical formatter.

~12 bead tasks in dependency order: lexer → parser → evaluator core → bindings →
functions/closures → control flow → errors → REPL → file runner → formatter →
polish/docs. The task list is authored once and copied verbatim into both arms.

### Anti-prior design

A conventional `let/fn/if/while` tree-walker is isomorphic to Lox (*Crafting
Interpreters*) and Monkey (*Writing an Interpreter in Go*), which saturate every
training corpus — a model can score well by autocompleting the tutorial rather
than reading the spec, and that headroom would mask exactly the effect under
test. Bract therefore deviates from tutorial priors in a fixed set of **prior
traps**: semantics that are *no harder to implement* when you know them
(avoiding floor effects), diametric to what Lox/Monkey/Python priors predict,
and mechanically checkable.

| # | Bract rule | The prior it contradicts |
|---|---|---|
| Q1 | `if`/`while` conditions must be booleans; anything else is `line N: condition must be a boolean` | Python/Lox truthiness |
| Q2 | `let` declares (redeclaration is an error); reassignment requires `set` | bare `=` for both |
| Q3 | String concatenation is `~`; `+` on strings is a type error | `+` concatenation |
| Q4 | Not-equals is `<>`; `!=` is a lex error | `!=` |
| Q5 | The string builtins (`at`, `len`, `sub`) are 1-indexed | 0-indexing |
| Q6 | Comparisons don't chain: `a < b < c` is a parse error | Python chaining |
| Q7 | `and`/`or` always evaluate both operands (no short-circuit) | short-circuit evaluation |

The quirks are in `DESIGN.MD` from day one, so they are **standing**
constraints: arm B's first scan turns them into injected requirements; arm A
must keep re-deriving them from the spec against the pull of its priors. The
drift constraints D1–D5 stay separately anti-prior for the same reason — a
drift rule the model's priors already agree with would let arm A comply by
accident and measure nothing.

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
| D4 | task 8 | "the REPL prompt is `bract> ` with a trailing space, and Ctrl-D exits cleanly" | tasks 9–12 |
| D5 | task 9 | "`bract fmt` output must be byte-stable (fmt∘fmt = fmt)" | tasks 10–12 |

In arm B these live only in a past task's prompt/transcript until the post-task
scan mines them into requirements; in arm A they exist only in history no later
step can see. **Metric:** for each later task touching the constraint's surface,
does the final merged code comply? Scored by automated checks (regex/AST/behavior
tests per constraint), reported as adherence rate per arm. Exact wording and
placement are frozen in the seed repo before any run.

### Co-primary: prior-trap pass rate

Of the hidden corpus, **~16 programs specifically exercise the prior traps**
(each quirk Q1–Q7 covered by at least two programs, including
negative-operand division and side-effect-visible `and`/`or` cases). This
sub-score is reported as its own metric: it measures whether the arm follows
the *spec* where the spec fights the model's *priors* — the standing-constraint
counterpart to drift adherence. A tutorial-prior implementation (Lox with the
serial numbers filed off) scores near zero here while passing much of the
general suite; that gap is the contamination-control working as intended.

### Secondary: hidden acceptance suite

A corpus of **~48 Bract programs with expected stdout/stderr/exit codes**
(the ~16 trap programs plus ~32 general-coverage programs), written against
the spec before any run and kept **outside both repos** (the agents never see
it). A harness script runs the corpus against each arm's final `main`; metric
is pass rate. Sub-scored by feature area so partial builds compare fairly.

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

1. **Freeze artifacts** (before any run): Bract spec + `DESIGN.md` with
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

## Ticket drafts (NOT yet filed)

E1–E6 were filed in bead on 2026-09-14 after the 3.21.0 install was verified.
**E7 and E8 stay out of the tracker permanently**: they are multi-hour arm runs
on the local GPU with a human decision gate between them — executed manually
per the Procedure section, not loop-claimable container work.

**E1 — Experiment seed repo + frozen Bract spec** *(feature, no deps)*
`experiments/intent-ab/seed/` in this repo (arms are cloned out to standalone
project dirs by the E5 driver; extraction stays within this project): project scaffold (Python, stdlib only),
`DESIGN.md` containing the full Bract spec with the seven prior traps Q1–Q7 and
~12 standing constraints, `.cloche/` setup shared by both arms (Dockerfile,
develop/host workflows, config.toml *without* arm-specific intent keys). Name
collision already checked. Acceptance: `cloche validate` passes; spec covers
every trap unambiguously; repo tagged `seed-v1` on freeze.

**E2 — Task list with embedded drift constraints** *(task, after E1)*
The 12 dependency-ordered task definitions (lexer → … → polish), authored once,
copied verbatim into both arms. Drift corrections D1–D5 embedded at their frozen
positions (tasks 4/5/6/8/9) with frozen wording, phrased as offhand corrections
inside otherwise-normal task descriptions. Acceptance: wording matches the
protocol table; a dry `list-tasks` run emits the 12 tasks in order.

**E3 — Hidden acceptance corpus + runner** *(feature, after E1)*
~48 Bract programs with expected stdout/stderr/exit codes: ≥2 per prior trap
(incl. negative-operand division, side-effect-visible `and`/`or`) plus ~32
general-coverage. Runner script executes the corpus against a repo's `main` and
emits overall, per-feature, and prior-trap pass rates as JSON. Lives outside the
seed repo (`experiments/intent-ab/eval/`), never copied into an arm. Acceptance: a
reference implementation (written for this purpose, also kept out of the arms)
passes 48/48; a deliberately Lox-prior implementation scores ≈0 on traps.

**E4 — Bonsai executor wrapper** *(feature, no deps)*
`agent_command` CLI driving bonsai-8b-16k through host Ollama's
OpenAI-compatible endpoint (candidate: aider non-interactive; else a minimal
edit-loop driver). Bakes in the spike's bonsai rules: temperature > 0, capped
generation length, strip leaked chain-of-thought before applying edits, retry
on empty content. Container networking via host-gateway + `network_allow`.
Acceptance: from inside a cloche container, the wrapper completes a trivial
scripted edit task against a fixture repo three times in a row.

**E5 — Arm configs + run orchestration** *(feature, after E1, E2, E4)*
Per-arm overlays (A: no intent dir, `scan_after_tasks = false`; B: defaults +
`intent.token_budget = 1000`) and a driver script: clone seed at `seed-v1`,
apply overlay, start loop, run to task-list exhaustion or budget/wall cap,
collect cloche process metrics (attempts, tokens, fix-loops, merges,
context-composition for B). Contamination asserts: arm A contains no
`.cloche/intent/` at any point; hidden corpus absent from both trees.
Acceptance: end-to-end smoke run of both arms against a 2-task stub list.

**E6 — Audit + judging tooling** *(feature, after E2, E3)*
Automated checkers for D1–D5 and the standing-constraint audit (regex/AST/
behavior per constraint, emitting per-constraint verdicts as JSON); blinded-
judging prep script (strip `.cloche/` and git history from copies, randomize
arm labels, emit judge bundle). Acceptance: checkers produce correct verdicts
against hand-made compliant and violating fixtures.

**E7 — Pilot run + calibration report** *(task, after E5, E6)*
Execute 1 run per arm; evaluate the calibration gate (unattended harness,
completion between ~30–90% in ≥1 arm, all metrics measurable); write the pilot
report to `docs/plans/spikes/` with a go/retune/no-go recommendation and, if
retuning, the specific task-list changes. Human decision point before
replications.

**E8 — Replications + final report** *(task, after E7, gated on pilot)*
3 runs per arm from `seed-v1` (or `seed-v2` if the pilot forced a retune),
full evaluation, medians + per-replication spreads, results doc in
`docs/plans/spikes/`, published write-up artifact.

## What would falsify the hypothesis

Arm B showing no improvement in drift adherence (primary), or buying adherence
at a hidden-suite cost exceeding it (context tax outweighing memory), across
replications. Either is a publishable result — it would say the feature needs
retuning for small-context executors (e.g., tighter budgets, fewer injected
requirements), which feeds directly back into `intent.token_budget` defaults.
