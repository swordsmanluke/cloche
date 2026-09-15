# Hidden acceptance corpus + runner (E3)

Part of the [Intent A/B experiment](../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
Everything in this directory is kept **outside** `experiments/intent-ab/seed/`
and is never copied into either experiment arm.

## Layout

- `corpus/` — 48 Bract programs with pinned expected `bract run` behavior
  (exit code, stdout, stderr), plus `manifest.json` describing them and
  `_generate.py`, the single source of truth that produces both. At least
  2 programs per prior trap (Q1–Q7, plus the D3 negative-division drift
  constant), ~32 general-coverage programs.
- `runner.py` — runs the corpus against any Bract checkout laid out like
  the seed repo (a directory containing a `bract/` package importable as
  `python3 -m bract run FILE`) and prints a JSON report: overall,
  per-feature-area, and prior-trap pass rates.
- `reference/` — a from-scratch Bract implementation written to conform
  exactly to `DESIGN.md` (including the D1–D5 drift corrections, since it
  validates the *final* spec). Passes the corpus 48/48.
- `lox_prior/` — a deliberately Lox/Python-prior calibration
  implementation ("Lox with the serial numbers filed off"): correct except
  for the eight trap dimensions (Q1–Q7, D3), where it does whatever a
  tutorial-shaped prior would predict instead of what `DESIGN.md`
  specifies. Scores 0/16 on the prior-trap subset while still passing most
  of the general suite — proof the trap subset actually discriminates.
- `audit/` (E6) — automated D1–D5 drift-constraint and SC1–SC12
  standing-constraint checkers, plus the blinded-judging prep script. See
  `audit/README.md`.
- `tests/` — `unittest`-based tests: acceptance-level checks that encode
  the criteria above (`test_corpus_acceptance.py`), unit tests against
  the reference implementation's lexer/parser/evaluator
  (`test_reference_impl.py`), and the E6 checker/judge-bundle acceptance
  tests (`test_audit_checks.py`, `test_judge_bundle.py`).

## Why no test checks `stdout` output from `bract run`

The language has no print/output builtin (`DESIGN.md` §6 only lists
`len`/`at`/`sub`), so a correct `bract run` never produces stdout on its
own. Value-level assertions in the corpus therefore use a self-verifying
pattern instead of printing:

```
if (2 + 2 <> 4) {
  let fail = 1 / 0;
}
```

A correct implementation exits 0 with empty stdout/stderr; a wrong one
hits the division and produces the pinned `line N: division by zero` /
exit 1 signature. This sidesteps needing an output mechanism the spec
doesn't define, while still exercising real computation.

## Usage

```
python3 _generate.py          # regenerate corpus/*.bract + manifest.json
                               # (run from corpus/, after editing _generate.py)

python3 runner.py --repo /path/to/bract/checkout
python3 runner.py --repo reference    # sanity check: expect 48/48
python3 runner.py --repo lox_prior    # sanity check: expect 0/16 on traps

python3 -m unittest discover -s tests -v
```
