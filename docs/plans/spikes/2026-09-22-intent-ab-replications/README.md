# Intent A/B — Replications on seed-v2 (2026-09-22)

**Status:** x4 replication 3 in progress; sections marked *(r3 pending)* are filled in
when it lands. Protocol: [`2026-09-14-intent-ab-experiment-protocol.md`](../../2026-09-14-intent-ab-experiment-protocol.md).
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
zero infrastructure events. Wall time per arm 32–43 min.

### Hidden corpus (48 programs) and prior-trap subset (16)

| run | arm A | arm B |
|---|---|---|
| round one (seed-v1, generic injection, manual recovery) | 37/48 · 12/16 | 40/48 · 14/16 |
| x3 r1 — control, generic injection | 48/48 · 16/16 | 47/48 · 16/16 |
| x4 r1 — task-specific injection | 47/48 · 16/16 | 46/48 · 16/16 |
| x4 r2 | 48/48 · 16/16 | 48/48 · 16/16 |
| x4 r3 | *(r3 pending)* | *(r3 pending)* |

The only recurring corpus failure is `cmp-ordering-type-error` (comparing mismatched
types with `<` doesn't raise), plus one `cf-nested-blocks-scope` in x4 arm B r1.
Haiku on seed-v2 is at ceiling; the corpus cannot separate the arms.

### Constraint audit (D1–D5 + SC1–SC12, corrected checkers)

| run | arm A | arm B |
|---|---|---|
| x3 r1 (control) | 15/17 — D2, SC3 | 15/17 — D2, SC3 |
| x4 r1 | 16/17 — D2 | 16/17 — D2 |
| x4 r2 | 16/17 — D2 | 15/17 — D2, SC3 |
| x4 r3 | *(r3 pending)* | *(r3 pending)* |

D2 fails everywhere for the reason above. SC3 (every error rendered through one shared
formatting path) is the one structural constraint that varies, and it varies in both
arms; there is no arm-B advantage on it.

### Extractor audit (arm B only; `eval/audit/intent_audit.py`, blind Sonnet judge)

| scan | reqs | seeded recall /24 | unseeded valid/total | dev-incident | dups | noise |
|---|---|---|---|---|---|---|
| round one arm B | 29 | 18 | 8/11 | 2 | 0 | 3 |
| x3 arm B r1 | 35 | 24 | 9/9 | 1 | 3 | 1 |
| x4 arm B r1 | 32 | 24 | 6/6 | 1 | 0 | 0 |
| x4 arm B r2 | 29 | 23 | 3/7 | 0 | 0 | 4 |
| x4 arm B r3 | *(r3 pending)* | | | | | |

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
| x4 A r3 / B r3 | *(r3 pending)* | | |

Zero marker drops in 72+ attempts, versus 25–54% of attempts in round one. The wiring
change removed the *consequence* of a drop, not the drop itself, and the monitor still
records drops as events — none occurred. Round one's drops concentrated on long,
checklist-shaped tasks under manual-recovery pressure; this round's runs were fast and
unattended. Not root-caused.

## Reading

- **The primary question is not answered by this round, and could not have been.**
  Both rounds' arm B injected generic constraints only (defect 2), and even with
  task-specific injection Haiku sits at the corpus ceiling on this task list. Whatever
  intent continuity does for a weaker model, seed-v2 + Haiku has no headroom to show it.
- **The secondary question has a clear answer.** The extractor recalls every planted
  constraint from a clean seed (24/24 twice), produces mostly valid unseeded
  requirements, and does extract rules from incidents (`req-a2c2`). It also extracts
  whatever else is in the history, including experiment scaffolding — a hygiene
  requirement on the seed, not a flaw in the mechanism.
- **The pipeline is now correct for the experiment**: task-specific injection (verified
  by preview and by the per-task prompt file), test-gated steps, infra monitors, and an
  audit with the false negatives/positives removed.

## Next

1. **Seed-v3:** D2 threshold above the recursion limit; overlay squashed and
   comment-free; keep the string-builtins assignment.
2. **Get off the ceiling** — one of: a longer or harder task list (the corpus needs
   room for arm A to fail), or a weaker hosted executor. The hypothesis is about
   weaker models; Haiku on 12 well-specified tasks is not weak enough to test it.
3. **Cloche:** `cloche-hbcv` (retrieval query for tracker tasks) is the one fix that
   matters for dogfooding — Cloche's own injection has been generic since the feature
   shipped.

## Artifacts

- Runs: `/home/lucas/workspace/bract-pilot/x4/` (report.json, results/*-scores.json,
  results/*-audit-v2.json, results/arm-b-r*-intent/), `x3/` (replication 1 = control).
- Extractor audits (judged): scratchpad `x4_b1_judged.json`, `x4_b2_judged.json`,
  `x3_b1_judged.json`, `round1_b_judged.json` — copied to `results/` on completion.
- Driver: `experiments/intent-ab/harness/arm_driver/` (`replicate.py`, `monitors.py`);
  extractor audit: `experiments/intent-ab/eval/audit/intent_audit.py`.
