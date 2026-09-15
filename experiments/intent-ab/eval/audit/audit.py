#!/usr/bin/env python3
"""Audit + judging tooling entry point (E6): runs the D1-D5 drift-constraint
checkers and the SC1-SC12 standing-constraint checkers against a Bract
implementation checkout and emits per-constraint verdicts as JSON.

Usage:
    python3 audit.py --repo /path/to/bract/checkout [--only D1,SC5] [--timeout 15]

The target is a directory containing an importable `bract/` package, laid
out like the seed repo or an arm's final `main` -- the same convention
../runner.py uses for the hidden acceptance corpus.
"""
import argparse
import json
import sys
from pathlib import Path

from checks.common import Verdict
from checks.drift import CHECKS as DRIFT_CHECKS
from checks.standing import CHECKS as STANDING_CHECKS

ALL_CHECKS = {**DRIFT_CHECKS, **STANDING_CHECKS}


def run_audit(repo: Path, only: set | None = None) -> dict:
    ids = sorted(ALL_CHECKS) if only is None else sorted(only)
    verdicts: list[Verdict] = []
    for check_id in ids:
        check_fn = ALL_CHECKS.get(check_id)
        if check_fn is None:
            verdicts.append(
                Verdict(id=check_id, method="n/a", passed=False, detail="unknown check id")
            )
            continue
        verdicts.append(check_fn(repo))

    drift = {v.id: v.to_dict() for v in verdicts if v.id in DRIFT_CHECKS}
    standing = {v.id: v.to_dict() for v in verdicts if v.id in STANDING_CHECKS}
    passed = sum(1 for v in verdicts if v.passed)
    return {
        "repo": str(repo),
        "drift": drift,
        "standing": standing,
        "summary": {
            "total": len(verdicts),
            "passed": passed,
            "failed": len(verdicts) - passed,
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--repo",
        required=True,
        type=Path,
        help="path to a Bract implementation checkout (root containing the bract/ package)",
    )
    parser.add_argument(
        "--only",
        type=str,
        default=None,
        help="comma-separated check ids to run (default: all of D1-D5, SC1-SC12)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=None,
        help="unused placeholder for a future per-check timeout override",
    )
    args = parser.parse_args()

    repo = args.repo.resolve()
    if not (repo / "bract").is_dir():
        print(f"error: {repo} does not contain a 'bract' package", file=sys.stderr)
        return 2

    only = set(s.strip() for s in args.only.split(",")) if args.only else None
    report = run_audit(repo, only)
    print(json.dumps(report, indent=2))
    return 0 if report["summary"]["failed"] == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
