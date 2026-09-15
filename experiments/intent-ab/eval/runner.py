#!/usr/bin/env python3
"""Hidden acceptance corpus runner (E3).

Runs experiments/intent-ab/eval/corpus/ against a target Bract
implementation's `main` (a directory laid out like the seed repo, i.e. one
importable as `python3 -m bract run FILE` from its root) and emits overall,
per-feature-area, and prior-trap pass rates as JSON.

Usage:
    python3 runner.py --repo /path/to/bract/checkout [--corpus DIR] [--json]

The corpus itself (corpus/*.bract, corpus/manifest.json) never gets copied
into either experiment arm -- only this script's *output* (a pass-rate
report) is used downstream by the evaluation step.
"""
import argparse
import json
import subprocess
import sys
from pathlib import Path

DEFAULT_TIMEOUT_SECONDS = 10


def load_manifest(corpus_dir: Path) -> list:
    with open(corpus_dir / "manifest.json") as f:
        return json.load(f)


def run_one(repo: Path, corpus_dir: Path, case: dict, timeout: float) -> dict:
    program_path = corpus_dir / case["file"]
    try:
        proc = subprocess.run(
            [sys.executable, "-m", "bract", "run", str(program_path)],
            cwd=str(repo),
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        exit_code = proc.returncode
        stdout = proc.stdout
        stderr = proc.stderr
        timed_out = False
    except subprocess.TimeoutExpired:
        exit_code = None
        stdout = ""
        stderr = ""
        timed_out = True

    failures = []
    if timed_out:
        failures.append(f"timed out after {timeout}s")
    else:
        if exit_code != case["expected_exit_code"]:
            failures.append(
                f"exit code: expected {case['expected_exit_code']}, got {exit_code}"
            )
        if stdout != case["expected_stdout"]:
            failures.append(
                f"stdout: expected {case['expected_stdout']!r}, got {stdout!r}"
            )
        check = case["stderr_check"]
        if check == "exact":
            if stderr != case["expected_stderr"]:
                failures.append(
                    f"stderr: expected {case['expected_stderr']!r}, got {stderr!r}"
                )
        elif check == "prefix":
            if not stderr.startswith(case["expected_stderr"]):
                failures.append(
                    f"stderr: expected prefix {case['expected_stderr']!r}, got {stderr!r}"
                )
        elif check != "none":
            raise ValueError(f"unknown stderr_check: {check!r}")

    return {
        "id": case["id"],
        "feature_area": case["feature_area"],
        "trap": case["trap"],
        "passed": not failures,
        "failures": failures,
    }


def summarize(results: list) -> dict:
    total = len(results)
    passed = sum(1 for r in results if r["passed"])

    by_feature = {}
    for r in results:
        bucket = by_feature.setdefault(r["feature_area"], {"total": 0, "passed": 0})
        bucket["total"] += 1
        bucket["passed"] += int(r["passed"])
    feature_area_pass_rate = {
        area: b["passed"] / b["total"] for area, b in sorted(by_feature.items())
    }

    trap_results = [r for r in results if r["trap"]]
    by_trap = {}
    for r in trap_results:
        bucket = by_trap.setdefault(r["trap"], {"total": 0, "passed": 0})
        bucket["total"] += 1
        bucket["passed"] += int(r["passed"])
    trap_pass_rate = {
        trap: b["passed"] / b["total"] for trap, b in sorted(by_trap.items())
    }
    trap_total = len(trap_results)
    trap_passed = sum(1 for r in trap_results if r["passed"])

    return {
        "overall": {
            "total": total,
            "passed": passed,
            "pass_rate": passed / total if total else 0.0,
        },
        "feature_area": {
            area: {
                "total": by_feature[area]["total"],
                "passed": by_feature[area]["passed"],
                "pass_rate": rate,
            }
            for area, rate in feature_area_pass_rate.items()
        },
        "prior_trap": {
            "overall": {
                "total": trap_total,
                "passed": trap_passed,
                "pass_rate": trap_passed / trap_total if trap_total else 0.0,
            },
            "by_trap": {
                trap: {
                    "total": by_trap[trap]["total"],
                    "passed": by_trap[trap]["passed"],
                    "pass_rate": rate,
                }
                for trap, rate in trap_pass_rate.items()
            },
        },
        "failures": [
            {"id": r["id"], "feature_area": r["feature_area"], "trap": r["trap"], "reasons": r["failures"]}
            for r in results
            if not r["passed"]
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo",
        required=True,
        type=Path,
        help="path to a Bract implementation checkout (root containing the bract/ package)",
    )
    parser.add_argument(
        "--corpus",
        type=Path,
        default=Path(__file__).parent / "corpus",
        help="path to the corpus directory (default: eval/corpus next to this script)",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=DEFAULT_TIMEOUT_SECONDS,
        help="per-program timeout in seconds (default: %(default)s)",
    )
    args = parser.parse_args()

    repo = args.repo.resolve()
    corpus_dir = args.corpus.resolve()

    if not (repo / "bract").is_dir():
        print(f"error: {repo} does not contain a 'bract' package", file=sys.stderr)
        return 2

    cases = load_manifest(corpus_dir)
    results = [run_one(repo, corpus_dir, case, args.timeout) for case in cases]
    report = summarize(results)
    report["repo"] = str(repo)
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
