"""Arm overlays + run orchestration driver (E5/E8) CLI.

Single-arm / pilot usage:
    python3 -m arm_driver --arm both --target-dir /tmp/intent-ab-smoke \\
        --task-list ../arms/stub-tasks.json --wall-cap-seconds 120

Replications (E8):
    python3 -m arm_driver --replications 3 --ref <seed-v2 sha> \\
        --target-dir /home/lucas/workspace/bract-pilot/rep --wall-cap-seconds 21600

Runs one or both arms end-to-end (see run_arm.run_arm), or N monitored
replications of both (see replicate.run_replications), and writes a JSON
report to --out (default: stdout for single runs; <target-dir>/report.json
for replications). Requires `cloche`, `bd`, and `git` on PATH, a reachable
cloche daemon, and Docker -- see ../README.md for the difference between
pointing this at a real pilot/replication run and the driver's own
fake-backed smoke test (../tests/test_driver_integration.py).
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
    parser.add_argument("--arm", choices=["a", "b", "both"], default="both")
    parser.add_argument(
        "--target-dir", required=True, type=Path,
        help="base directory; arm-a/ and/or arm-b/ (or arm-<x>-r<n>/ for replications) are created under it",
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

    rep = parser.add_argument_group("replications (E8)")
    rep.add_argument("--replications", type=int, default=0,
                     help="run this many monitored arm-A/arm-B pairs (0 = single-run mode)")
    rep.add_argument("--start-at", type=int, default=1, help="first replication number (resume)")
    rep.add_argument("--sequential-arms", action="store_true",
                     help="run arm A then arm B within a replication instead of concurrently")
    rep.add_argument("--max-attempts-per-task", type=int, default=4,
                     help="stop an arm once one task has failed this many attempts (execution outcome)")
    rep.add_argument("--stall-seconds", type=float, default=2700.0,
                     help="abort an arm (infra) after this long with no activity")
    rep.add_argument("--daemon-log", default="/tmp/cloched.log")
    rep.add_argument("--no-score", action="store_true", help="skip corpus/audit scoring of finished arms")
    return parser.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)

    if args.replications > 0:
        from .replicate import run_replications
        report = run_replications(
            args.target_dir, args.replications, args.ref,
            task_list=args.task_list, seed_source=args.seed_source,
            eval_corpus_dir=args.eval_corpus_dir, parallel_arms=not args.sequential_arms,
            wall_cap_seconds=args.wall_cap_seconds, poll_interval_seconds=args.poll_interval_seconds,
            max_attempts_per_task=args.max_attempts_per_task, stall_seconds=args.stall_seconds,
            daemon_log_path=args.daemon_log, score=not args.no_score, start_at=args.start_at,
        )
        if args.out:
            args.out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
        return 0 if not report.get("halted") else 2

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
