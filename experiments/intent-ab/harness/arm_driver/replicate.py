"""Replications (E8): N fresh arm-A / arm-B pairs from one frozen seed ref,
each arm monitored for infrastructure failures, each finished arm scored
with the hidden corpus and the constraint audit, everything written to one
incrementally-updated report.

Within a replication the two arms run concurrently (two loops, two
projects, one container each); replications run one after another. An
arm that stops for an infrastructure reason stops its sibling too and ends
the whole run — infra problems need a human before more budget burns. An
arm that stops for an execution reason (task list exhausted, per-task
attempt cap) is data, and the next replication proceeds.
"""
import json
import shutil
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from pathlib import Path

from . import monitors
from .cloche_cli import Toolchain
from .run_arm import (DEFAULT_EVAL_CORPUS_DIR, DEFAULT_SEED_SOURCE, DEFAULT_TASK_LIST,
                      HARNESS_ROOT, run_arm)

EVAL_ROOT = HARNESS_ROOT.parent / "eval"


def _now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _log(log, msg):
    log(f"[{_now()}] {msg}")


def score_arm(target_dir: Path, eval_root: Path = EVAL_ROOT, timeout: int = 900) -> dict:
    """Hidden corpus + drift/standing audit against the arm's final tree.
    Both tools print JSON; a tool failure is recorded, not raised."""
    out = {}
    for name, script in (("corpus", eval_root / "runner.py"), ("audit", eval_root / "audit" / "audit.py")):
        try:
            r = subprocess.run([sys.executable, str(script), "--repo", str(target_dir)],
                               capture_output=True, text=True, timeout=timeout)
            # Both tools exit non-zero when a check/program fails; the JSON
            # report on stdout is the result either way.
            try:
                out[name] = json.loads(r.stdout)
            except json.JSONDecodeError:
                out[name] = {"error": (r.stderr.strip() or r.stdout.strip())[-2000:], "returncode": r.returncode}
        except subprocess.TimeoutExpired as exc:
            out[name] = {"error": repr(exc)}
    return out


def snapshot_intent(target_dir: Path, results_dir: Path, name: str):
    src = target_dir / ".cloche" / "intent"
    if src.is_dir():
        dst = results_dir / f"{name}-intent"
        if dst.exists():
            shutil.rmtree(dst)
        shutil.copytree(src, dst, ignore=shutil.ignore_patterns("*.sock"))
        return str(dst)
    return None


def _write_report(path: Path, report: dict):
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    tmp.replace(path)


def run_replications(base_dir: Path, replications: int, ref: str,
                     task_list: Path = None, seed_source: Path = DEFAULT_SEED_SOURCE,
                     eval_corpus_dir: Path = DEFAULT_EVAL_CORPUS_DIR,
                     parallel_arms: bool = True, wall_cap_seconds: float = 21600.0,
                     poll_interval_seconds: float = 10.0, max_attempts_per_task: int = 4,
                     stall_seconds: float = 2700.0, daemon_log_path: str = "/tmp/cloched.log",
                     score: bool = True, start_at: int = 1, toolchain_factory=Toolchain,
                     log=print) -> dict:
    base_dir = Path(base_dir)
    base_dir.mkdir(parents=True, exist_ok=True)
    results_dir = base_dir / "results"
    results_dir.mkdir(exist_ok=True)
    report_path = base_dir / "report.json"
    task_list = Path(task_list).resolve() if task_list else DEFAULT_TASK_LIST

    report = {
        "ref": ref, "task_list": str(task_list), "replications": replications,
        "started": _now(), "arms": [], "halted": None,
    }
    if report_path.exists():
        try:
            prior = json.loads(report_path.read_text())
            report["arms"] = prior.get("arms", [])
        except json.JSONDecodeError:
            pass
    _write_report(report_path, report)

    def monitor_factory(tc):
        return monitors.InfraMonitor(
            tc, poll_interval_seconds=poll_interval_seconds, stall_seconds=stall_seconds,
            max_attempts_per_task=max_attempts_per_task, daemon_log_path=daemon_log_path,
        )

    for rep in range(start_at, replications + 1):
        infra_stop = threading.Event()
        outcomes = {}

        def one(arm):
            name = f"arm-{arm}-r{rep}"
            target = base_dir / name
            _log(log, f"{name}: starting (ref {ref})")
            try:
                r = run_arm(
                    arm, target, task_list=task_list, seed_source=seed_source, ref=ref,
                    eval_corpus_dir=eval_corpus_dir, poll_interval_seconds=poll_interval_seconds,
                    wall_cap_seconds=wall_cap_seconds, toolchain_factory=toolchain_factory,
                    monitor_factory=monitor_factory, should_stop=infra_stop.is_set,
                )
            except Exception as exc:  # setup failures are infra too
                r = {"arm": arm, "target_dir": str(target), "ref": ref,
                     "stop_reason": "infra:driver_exception", "error": repr(exc), "events": []}
                infra_stop.set()
            r["name"] = name
            r["replication"] = rep
            r["finished"] = _now()
            reason = r.get("stop_reason") or ""
            if reason.startswith("infra:"):
                infra_stop.set()
            _log(log, f"{name}: stopped — {reason}")
            if score and Path(r["target_dir"]).is_dir():
                _log(log, f"{name}: scoring")
                r["scores"] = score_arm(Path(r["target_dir"]))
                (results_dir / f"{name}-scores.json").write_text(json.dumps(r["scores"], indent=2) + "\n")
            if arm == "b":
                r["intent_snapshot"] = snapshot_intent(Path(r["target_dir"]), results_dir, name)
            outcomes[arm] = r

        if parallel_arms:
            threads = [threading.Thread(target=one, args=(arm,), name=f"rep{rep}-{arm}") for arm in ("a", "b")]
            for t in threads:
                t.start()
                time.sleep(3)  # stagger project registration / image checks
            for t in threads:
                t.join()
        else:
            for arm in ("a", "b"):
                one(arm)
                if infra_stop.is_set():
                    break

        for arm in ("a", "b"):
            if arm in outcomes:
                report["arms"].append(outcomes[arm])
        _write_report(report_path, report)

        if infra_stop.is_set():
            report["halted"] = {"replication": rep, "at": _now(),
                                "reason": "an arm stopped for an infrastructure reason; see its events"}
            _write_report(report_path, report)
            _log(log, f"replication {rep}: halted on infrastructure failure — not starting further replications")
            break

    report["finished"] = _now()
    _write_report(report_path, report)
    return report
