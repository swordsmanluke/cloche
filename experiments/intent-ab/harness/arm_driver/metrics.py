"""Process metrics from cloche's own stores (protocol doc, "Process
metrics" section): tasks completed/attempted, attempts per task, fix-loop
iterations, tokens, merges; context composition for arm B.

Everything here is derived from `cloche activity --json` (structured,
documented) plus two best-effort text scrapes (`cloche status <task-id>`'s
`Tokens:` line, `cloche intent preview`'s rendered block) for the two
numbers no CLI command exposes structurally as of this writing — per-task
token totals and per-step injected-block size. Both are clearly labeled
"_est"/approximate in the report; see run_arm.py's docstring for why a
true per-step token *share* isn't obtainable through the CLI surface alone
(cmd/cloche/main.go's own printTaskTokenUsage comment: GetUsage has no
task/attempt scoping, and per-step StepExecutions token counts are never
surfaced outside the gRPC GetStatus response).
"""
import re

FIX_LOOP_STEPS = {"fix-tests", "fix-merge"}

_TOKENS_RE = re.compile(r"Tokens:\s+([\d,]+)")


def summarize_activity(entries: list) -> dict:
    attempts_per_task = {}
    ended_states = {}
    fix_loop_iterations = 0
    merges_succeeded = 0
    merges_failed = 0

    for entry in entries:
        kind = entry.get("kind")
        task_id = entry.get("task_id") or ""
        if kind == "attempt_started" and task_id:
            attempts_per_task[task_id] = attempts_per_task.get(task_id, 0) + 1
        elif kind == "attempt_ended" and task_id:
            ended_states.setdefault(task_id, []).append(entry.get("state", ""))
        elif kind == "step_started" and entry.get("step") in FIX_LOOP_STEPS:
            fix_loop_iterations += 1
        elif kind == "step_completed" and entry.get("step") == "merge":
            if entry.get("result") == "success":
                merges_succeeded += 1
            else:
                merges_failed += 1

    # A task may be attempted more than once (resume/retry); its outcome is
    # whatever its most recent attempt_ended entry says.
    tasks_succeeded = sum(1 for states in ended_states.values() if states and states[-1] == "succeeded")
    tasks_failed = sum(1 for states in ended_states.values() if states and states[-1] != "succeeded")

    return {
        "tasks_attempted": len(attempts_per_task),
        "tasks_succeeded": tasks_succeeded,
        "tasks_failed": tasks_failed,
        "total_attempts": sum(attempts_per_task.values()),
        "attempts_per_task": attempts_per_task,
        "fix_loop_iterations": fix_loop_iterations,
        "merges_succeeded": merges_succeeded,
        "merges_failed": merges_failed,
    }


def parse_tokens_line(status_text):
    """Best-effort scrape of `cloche status <task-id>`'s `Tokens:  N (...)`
    line (cmd/cloche/main.go's printTaskTokenUsage). Returns None if the
    line is absent (no usage data for the task) or `status_text` is None."""
    if not status_text:
        return None
    match = _TOKENS_RE.search(status_text)
    if not match:
        return None
    return int(match.group(1).replace(",", ""))


def estimate_injected_block_size(preview_text):
    """Approximate size of the `## Standing project requirements` block
    `cloche intent preview` would render for a given workflow/step, as a
    proxy for the context-composition tax (protocol doc). Word count is a
    coarse tokens proxy (no tokenizer dependency here); report both raw
    counts so downstream analysis can apply whatever conversion it trusts.
    Returns None if preview_text is None (e.g. arm A, where injection is
    dormant, or the preview call failed)."""
    if preview_text is None:
        return None
    return {
        "chars": len(preview_text),
        "words_est": len(preview_text.split()),
    }
