# Intent A/B — seed-v3 design: harder tasks (proposal)

**Status:** proposal, 2026-09-22. Follows the [replications results](spikes/2026-09-22-intent-ab-replications/README.md);
amends the [protocol](2026-09-14-intent-ab-experiment-protocol.md). Decision taken: get
off the ceiling with a harder task list, not a weaker executor.

## Why harder, and harder how

On seed-v2 Haiku scores 48/48 on the hidden corpus in both arms; the only
between-arm movement in three replications was one drift correction lost by the arm
without the memory. The task list is well-specified and each task is self-contained, so
a capable model does not need to remember anything to do well.

"Harder" therefore does not mean more code. It means **more opportunities to forget,
and tasks whose easiest implementation is wrong unless an earlier correction is
remembered.** Three levers:

1. **Longer horizon:** 12 → 20 tasks, so mid-project corrections have more later tasks
   to survive.
2. **Trap tasks:** each new task has a plausible, locally-optimal implementation that
   violates a constraint introduced earlier — a drift correction from a previous task's
   aside, or a standing constraint from DESIGN.md — and a correct implementation that
   costs a little more. The memory's job is exactly to make the correct one obvious.
3. **More drift corrections:** D1–D5 → D1–D10, still delivered as offhand asides in
   task prompts, never in DESIGN.md, each with a later task that presses on it.

DESIGN.md stays frozen as written (seed-v2 text). New features are specified in the
task prompts, so their details are also things that have to be carried forward.
Reference implementation, hidden corpus and audit checks are extended and frozen
before any run, per the protocol's pre-registration rule.

## Task list (20)

Tasks 1–10 are seed-v2's, with two changes: task 5's deep-chain test depth becomes
**5,000 frames** (above Python's 1,000 recursion limit; seed-v2 said "hundreds", which a
naive recursive evaluator passes while failing the 2,500-deep audit), and task 3 keeps
the string builtins. Polish and docs move to the end as 19 and 20. New tasks 11–18:

| # | task | what it asks for | the trap (constraint it presses on) | new drift aside |
|---|---|---|---|---|
| 11 | error-context | Runtime errors inside a function report the line of the failing expression, and `bract run` shows the offending source line's text in the message. | D1/SC3: the obvious implementation prints a second line with the source text, or a caret line. Correct: one line, e.g. `line 7: division by zero — in: let y = 1 / 0;`, through the shared formatter. | **D6:** the reported `N` is the innermost expression's line, never the call site's. |
| 12 | check-subcommand | `bract check FILE`: lex+parse only, print nothing on success, report the first error per §8. | SC10 (single `__main__` dispatch), SC6 (exit codes stay `{0,1,2}` — tempting to invent `3` for "checked with errors"; correct is `2`), D1. | — |
| 13 | perf | Tight-loop benchmark fixture (`bench/loop.bract`, 200k iterations) must run under 2 s; "optimize the evaluator's hot path". | **D2** (rewriting the explicit call stack into Python recursion is the fastest fix), SC8 (caching in module globals), SC7 (compiling to Python and `exec`-ing). | **D9:** deep-chain depth test is now 10,000 frames; still must pass. |
| 14 | repl-multiline | REPL reads multi-line statements: an incomplete statement (unbalanced braces/parens) shows a continuation prompt and keeps reading. | D4 (primary prompt must remain exactly `bract> `; EOF mid-continuation still exits 0 cleanly). | **D7:** the continuation prompt is `...... ` (six dots, one space). |
| 15 | fmt-comments | `bract fmt` preserves `//` comments in place (currently dropped). | SC9 (formatter changes only whitespace/indent/braces — comments must not move relative to code), D5 (idempotence must still hold with comments). | **D10:** fmt output ends with exactly one `\n`; CRLF input is normalised. |
| 16 | run-args | `bract run FILE -- a b c` exposes program args via builtins `argc()` and `arg(i)`. | Q5 (1-indexed: `arg(1)` is the first argument; `arg(0)` is out of range), SC6 (a bad index is a *runtime* error, exit 1 — not a usage error). | **D8:** every builtin is registered in one table in the evaluator; no builtin is special-cased in the parser or CLI. |
| 17 | trace-flag | `bract run --trace FILE` writes `trace: line N` to stderr before each top-level statement executes. | SC4 (no stderr writes outside the CLI module — trace output must route through the CLI/formatter layer, not `sys.stderr.write` in the evaluator), D1 (error lines must still be exactly one `line N:` line and distinguishable from trace lines). | — |
| 18 | repl-load | REPL `:load FILE` executes a file into the current session; `:quit` exits. | D4 + D7 (both prompts unchanged), D6 (errors from a loaded file report the file's line numbers), SC10. | — |
| 19 | polish | seed-v2's task 11, extended to SC1–SC12 plus everything tasks 11–18 introduced. | — | — |
| 20 | docs | seed-v2's task 12. | — | — |

Dependency chain is linear, as before.

## Measurement

- **Primary:** constraint retention — D1–D10 (behavioural checks; D6–D10 new) and
  SC1–SC12 (existing checkers, corrected). Reported per arm as constraints lost, with
  drift and standing separated, medians across replications.
- **Secondary:** hidden corpus, extended from 48 to ~80 programs to cover tasks 11–18
  and each trap's correct outcome (a runtime error inside a function still one line;
  `check` exit codes; `arg(0)` out of range; comments preserved and idempotent; trace
  lines interleaved with an error line; `:load` line numbers).
- **Tertiary:** blinded pairwise judging, and the extractor audit for arm B (seeded
  recall now over Q1–Q7, D1–D10, SC1–SC12; unseeded quality as before).
- **Process:** as in x4 (attempts, marker drops, wall time), plus per-task "injected
  requirement IDs" now that retrieval is task-specific (from the KV keys, read at
  dispatch by a driver hook — the values are not readable after the attempt ends).

## Reference implementation and freeze

- `eval/reference/` gains tasks 11–18 (check, --trace, args, multi-line REPL, `:load`,
  comment-preserving fmt, error context) and must pass the extended corpus 80/80 and
  the extended audit 22/22 before the freeze.
- New audit checks D6–D10 in `eval/audit/checks/drift.py`, each with a compliant and a
  violating fixture.
- Freeze: `seed-v3 = <sha>` recorded in the protocol; overlays unchanged from
  `dca839e5`+hygiene (comment-free, folded into the seed commit).

## Expected effect, and what would falsify it

If intent continuity works as hypothesised, arm B should lose fewer of D1–D10 than arm A
across replications, concentrated on the trap tasks (13, 14, 17, 18). If both arms lose
the same drift constraints at the same rate — as they did the standing constraints in
x4 — then either Haiku does not drift on this list even without memory (raise the
horizon or lower the executor), or injection is present but not followed (which the
per-task injected-ID record now makes attributable).

## Cost

Roughly: reference implementation and corpus/audit extension ~1 day of agent time;
each replication ~70 min per arm at Haiku's current pace; 3 replications ≈ 4 h
unattended.
