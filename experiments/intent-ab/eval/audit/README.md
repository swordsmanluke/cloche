# Audit + judging tooling (E6)

Part of the [Intent A/B experiment](../../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
Two independent tools:

- **`audit.py`** — runs the drift-constraint (D1-D5) and standing-constraint
  (SC1-SC12) checkers against a Bract implementation checkout and prints a
  JSON verdict report.
- **`judge_bundle.py`** — blinded-judging prep: copies both arms' final
  trees, strips `.cloche/` and git history, randomizes which copy is which,
  and emits a judge bundle plus a separate key file.

## `audit.py`

```
python3 audit.py --repo /path/to/bract/checkout
python3 audit.py --repo /path/to/bract/checkout --only D1,D2,SC5
```

The target is a directory containing an importable `bract/` package, same
convention as `../runner.py` uses for the hidden acceptance corpus. Output:

```json
{
  "repo": "...",
  "drift": { "D1": {"id": "D1", "method": "behavior", "passed": true, "detail": "...", "evidence": {}}, ... },
  "standing": { "SC1": {...}, ... },
  "summary": {"total": 17, "passed": 17, "failed": 0}
}
```

Exit code is 0 if every requested check passed, 1 if any failed, 2 for a
usage/setup error (e.g. `--repo` doesn't contain a `bract/` package).

Each checker in `checks/drift.py` / `checks/standing.py` uses whichever of
regex, AST, or subprocess behavior probing fits the constraint best (see
each function's docstring). Behavior checks run the target's own
`python3 -m bract run|fmt` CLI against small self-verifying probe programs
under a `.audit_tmp/` scratch directory inside the target repo (mirroring
`../runner.py`'s invocation convention) rather than asserting on exact
output text, so they work against any conforming implementation, not just
ones that happen to phrase error messages identically to the reference.

**SC4 note:** DESIGN.md sec10 names `bract/cli.py` as the one file allowed
to print, but nothing in the seed repo, the reference implementation, or
the task list ever creates a `cli.py` — the actual CLI entry point
everywhere is `bract/__main__.py` (matching SC10's own wording). That looks
like a leftover inconsistency in the frozen spec; `check_sc4` treats both
`cli.py` and `__main__.py` as the allowed CLI layer rather than picking one
unilaterally. See `checks/standing.py`'s module docstring.

## `judge_bundle.py`

```
python3 judge_bundle.py --arm-a /path/to/arm/A --arm-b /path/to/arm/B --out /path/to/bundle [--seed N]
```

Produces `<out>/tree_1/`, `<out>/tree_2/` (each arm's tree with `.cloche/`
and `.git/` removed) and `<out>/MANIFEST.json` — that directory is what
gets handed to the judge. The real `tree_1`/`tree_2` -> A/B mapping is
written to a sibling file, `<out-parent>/<out-name>.key.json`, kept
*outside* the bundle directory so it isn't accidentally shared with the
judge. `--seed` makes the label shuffle reproducible for a given run.

## Fixtures

`fixtures/compliant/` is a small, genuinely-working Bract interpreter that
satisfies every D1-D5 / SC1-SC12 check. `fixtures/violations/<ID>/` is that
same tree with one targeted patch applied, built by
`fixtures/build_violations.py` (re-run it after editing a patch). See
`fixtures/README.md` for the one deliberate shortcut in the compliant tree
(D2's iterativeness proxy) and `tests/test_audit_checks.py` (in `../tests/`)
for the acceptance tests that run the full checker suite against every
fixture and assert exactly the expected pass/fail pattern.

## Usage

```
python3 -m unittest discover -s ../tests -v    # from this directory
# or, from eval/:
python3 -m unittest tests.test_audit_checks tests.test_judge_bundle -v
```
