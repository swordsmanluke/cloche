import subprocess
"""Run one arm end-to-end (E5): clone seed at a ref, wire in the bonsai
wrapper, apply the arm overlay, register the project, bootstrap the task
tracker, start the loop, poll to task-list exhaustion or a budget/wall cap
(re-checking the contamination invariants on every tick), stop the loop,
and collect process metrics.

See ../arms/README.md for the overlay composition and
../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md ("Process
metrics") for what's measured and why some of it (per-task tokens, the
context-composition tax) is a best-effort text scrape rather than a
structured query — no CLI command exposes those numbers directly; see
metrics.py's docstring.
"""
import time
from pathlib import Path

from . import contamination, metrics, overlay, seed
from .cloche_cli import Toolchain

HARNESS_ROOT = Path(__file__).resolve().parent.parent
ARMS_ROOT = HARNESS_ROOT / "arms"
DEFAULT_SEED_SOURCE = HARNESS_ROOT.parent / "seed"
DEFAULT_EVAL_CORPUS_DIR = HARNESS_ROOT.parent / "eval" / "corpus"
DEFAULT_REF = "seed-v1"
DEFAULT_TASK_LIST = DEFAULT_SEED_SOURCE / ".cloche" / "tasks" / "seed-tasks.json"

# The seed's one host-orchestrated container workflow and its two prompt
# steps (seed/.cloche/develop.cloche) -- fixed by the seed, not arm-specific.
ARM_WORKFLOW = "develop"
ARM_STEPS = ("implement", "fix-tests")


class ArmRunError(RuntimeError):
    pass


class StopReason:
    EXHAUSTED = "task_list_exhausted"
    WALL_CAP = "wall_clock_cap"
    ATTEMPT_CAP = "attempt_budget_cap"


def overlay_dirs_for(arm: str):
    if arm not in ("a", "b"):
        raise ValueError(f"unknown arm: {arm!r} (expected 'a' or 'b')")
    return [ARMS_ROOT / "common", ARMS_ROOT / f"arm-{arm}"]


def setup_arm_tree(arm: str, target_dir: Path, seed_source: Path = DEFAULT_SEED_SOURCE,
                    ref: str = DEFAULT_REF) -> None:
    """Clone seed at `ref` into `target_dir`, then layer in the bonsai
    wrapper's live source and the arm overlay (../arms/README.md's
    composition order)."""
    seed.clone_seed(seed_source, ref, target_dir)
    overlay.copy_into(target_dir, "agent_command", HARNESS_ROOT / "agent_command")
    overlay.copy_into(target_dir, "bin", HARNESS_ROOT / "bin")
    overlay.apply_overlay(target_dir, overlay_dirs_for(arm))
    # Containers seed from a clean git snapshot of HEAD (req-065f):
    # uncommitted overlay files would be invisible in-container, so the
    # composed tree must be committed before any run is dispatched.
    subprocess.run(["git", "add", "-A"], cwd=target_dir, check=True)
    subprocess.run(
        ["git", "commit", "-q", "-m", f"arm overlay: wrapper + arm-{arm} config"],
        cwd=target_dir, check=True,
    )


def assert_clean(arm: str, target_dir: Path, eval_corpus_dir: Path = DEFAULT_EVAL_CORPUS_DIR) -> None:
    if arm == "a":
        contamination.assert_no_intent_dir(target_dir)
    contamination.assert_no_eval_corpus(target_dir, eval_corpus_dir)


def bootstrap_tracker(tc: Toolchain, task_list: Path) -> None:
    tc.bd_init()
    tc.bd_create_graph(task_list)


def run_to_completion(tc: Toolchain, arm: str, target_dir: Path,
                       eval_corpus_dir: Path = DEFAULT_EVAL_CORPUS_DIR,
                       poll_interval_seconds: float = 5.0,
                       wall_cap_seconds: float = 3600.0,
                       max_attempts=None,
                       sleep=time.sleep, clock=time.monotonic) -> dict:
    """Start the loop, poll until the task list is exhausted or a cap
    fires, then stop the loop. `bd ready --json` is the exhaustion check:
    it lists both open and in_progress tasks (get-tasks.py's own model of
    readiness), so an empty result means every task is closed, not merely
    that nothing is claimable this instant."""
    assert_clean(arm, target_dir, eval_corpus_dir)
    tc.loop_start()
    start = clock()
    stop_reason = None
    try:
        while True:
            assert_clean(arm, target_dir, eval_corpus_dir)

            if clock() - start >= wall_cap_seconds:
                stop_reason = StopReason.WALL_CAP
                break

            if max_attempts is not None:
                attempted = metrics.summarize_activity(tc.activity_json())["total_attempts"]
                if attempted >= max_attempts:
                    stop_reason = StopReason.ATTEMPT_CAP
                    break

            if not tc.bd_ready():
                stop_reason = StopReason.EXHAUSTED
                break

            sleep(poll_interval_seconds)
    finally:
        tc.loop_stop()

    assert_clean(arm, target_dir, eval_corpus_dir)
    return {"stop_reason": stop_reason, "elapsed_seconds": clock() - start}


def collect_metrics(tc: Toolchain, arm: str) -> dict:
    entries = tc.activity_json()
    summary = metrics.summarize_activity(entries)
    task_ids = list(summary["attempts_per_task"])

    per_task_tokens = {}
    known_total = 0
    any_known = False
    for task_id in task_ids:
        tokens = metrics.parse_tokens_line(tc.status_text(task_id))
        per_task_tokens[task_id] = tokens
        if tokens is not None:
            known_total += tokens
            any_known = True
    summary["tokens_per_task"] = per_task_tokens
    summary["tokens_total"] = known_total if any_known else None

    if arm == "b":
        summary["context_composition"] = {
            step: metrics.estimate_injected_block_size(tc.intent_preview(ARM_WORKFLOW, step))
            for step in ARM_STEPS
        }
        summary["injected_requirement_ids_per_task"] = {
            task_id: {
                step: tc.kv_get(task_id, f"{ARM_WORKFLOW}:{step}:intent")
                for step in ARM_STEPS
            }
            for task_id in task_ids
        }

    return summary


def run_arm(arm: str, target_dir: Path, task_list: Path = None,
            seed_source: Path = DEFAULT_SEED_SOURCE, ref: str = DEFAULT_REF,
            eval_corpus_dir: Path = DEFAULT_EVAL_CORPUS_DIR,
            poll_interval_seconds: float = 5.0, wall_cap_seconds: float = 3600.0,
            max_attempts=None, toolchain_factory=Toolchain,
            sleep=time.sleep, clock=time.monotonic) -> dict:
    """Full procedure for one arm. Returns a JSON-serializable report."""
    target_dir = Path(target_dir)
    task_list = Path(task_list).resolve() if task_list is not None else DEFAULT_TASK_LIST

    setup_arm_tree(arm, target_dir, seed_source=seed_source, ref=ref)
    assert_clean(arm, target_dir, eval_corpus_dir)

    tc = toolchain_factory(target_dir)
    tc.cloche_init()
    bootstrap_tracker(tc, task_list)

    run_result = run_to_completion(
        tc, arm, target_dir, eval_corpus_dir=eval_corpus_dir,
        poll_interval_seconds=poll_interval_seconds, wall_cap_seconds=wall_cap_seconds,
        max_attempts=max_attempts, sleep=sleep, clock=clock,
    )
    process_metrics = collect_metrics(tc, arm)

    return {
        "arm": arm,
        "target_dir": str(target_dir),
        "task_list": str(task_list),
        "ref": ref,
        **run_result,
        "metrics": process_metrics,
    }
