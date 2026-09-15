"""Arm overlays + run orchestration driver (E5) CLI.

Usage:
    python3 -m arm_driver --arm both --target-dir /tmp/intent-ab-smoke \\
        --task-list ../arms/stub-tasks.json --wall-cap-seconds 120

Runs one or both arms end-to-end (see run_arm.run_arm) and writes a JSON
report to --out (default: stdout). Requires `cloche`, `bd`, and `git` on
PATH, a reachable cloche daemon, and Docker -- see ../README.md for the
difference between pointing this at a real pilot/replication run and the
driver's own fake-backed smoke test (../tests/test_driver_integration.py),
which is what actually exercises this CLI in an environment without those.
"""
import argparse
import json
import sys
from pathlib import Path

from .run_arm import DEFAULT_EVAL_CORPUS_DIR, DEFAULT_REF, DEFAULT_SEED_SOURCE, DEFAULT_TASK_LIST, run_arm


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="arm_driver",
        description="Clone the Intent A/B seed, apply an arm overlay, and run it to task-list exhaustion or a cap.",
    )
    parser.add_argument("--arm", choices=["a", "b", "both"], required=True)
    parser.add_argument(
        "--target-dir", required=True, type=Path,
        help="base directory; arm-a/ and/or arm-b/ are created under it",
    )
    parser.add_argument("--task-list", type=Path, default=DEFAULT_TASK_LIST)
    parser.add_argument("--seed-source", type=Path, default=DEFAULT_SEED_SOURCE)
    parser.add_argument("--ref", default=DEFAULT_REF)
    parser.add_argument("--eval-corpus-dir", type=Path, default=DEFAULT_EVAL_CORPUS_DIR)
    parser.add_argument("--poll-interval-seconds", type=float, default=5.0)
    parser.add_argument("--wall-cap-seconds", type=float, default=3600.0)
    parser.add_argument(
        "--max-attempts", type=int, default=None,
        help="stop once this many total attempts have been dispatched (budget cap)",
    )
    parser.add_argument("--out", type=Path, default=None, help="write JSON report here (default: stdout)")
    return parser.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    arms = ["a", "b"] if args.arm == "both" else [args.arm]

    reports = []
    for arm in arms:
        reports.append(run_arm(
            arm,
            args.target_dir / f"arm-{arm}",
            task_list=args.task_list,
            seed_source=args.seed_source,
            ref=args.ref,
            eval_corpus_dir=args.eval_corpus_dir,
            poll_interval_seconds=args.poll_interval_seconds,
            wall_cap_seconds=args.wall_cap_seconds,
            max_attempts=args.max_attempts,
        ))

    output = {"arms": reports} if args.arm == "both" else reports[0]
    text = json.dumps(output, indent=2, sort_keys=True)
    if args.out:
        args.out.write_text(text + "\n")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
