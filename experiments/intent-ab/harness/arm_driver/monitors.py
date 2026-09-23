"""Infra/setup monitors for the replication driver (E8).

A replication is only valid data if the arm stopped for an *execution*
reason — the task list ran out, or a task exhausted its retry budget — and
never silently absorbed an *infrastructure* failure: a daemon that went
away, an expired agent credential, an image that failed to build, an
extraction branch that vanished between develop and merge (the round-one
cloche-8od1 bug), a loop the daemon halted for a container problem. This
module classifies what the poll loop sees into those two buckets.

Execution outcomes are recorded as events and counted (marker drops, step
failures, retries). Infrastructure outcomes abort the arm so a human looks
before more budget burns. One exception, deliberately narrow: the known
stale-slot bug (loop shows work queued for "capacity" while no slot is
busy) is benign to retry, so it gets one automatic loop restart, logged as
an event, before it too aborts.

Everything here is a pure function of text the toolchain already exposes
(`cloche status`, `cloche activity --json`, `cloche logs`), so it is unit
testable without a daemon.
"""
import re
import time


class Kind:
    INFRA_DAEMON = "infra:daemon_unreachable"
    INFRA_AUTH = "infra:agent_auth"
    INFRA_IMAGE = "infra:image_build"
    INFRA_EXTRACTION = "infra:extraction_branch_missing"
    INFRA_LOOP_HALTED = "infra:loop_halted"
    INFRA_STALE_SLOT = "infra:stale_slot"
    INFRA_STALL = "infra:no_progress"
    INFRA_INTENT_STORE = "infra:intent_store_invalid"
    EXEC_MARKER_DROP = "exec:marker_drop"
    EXEC_STEP_FAILED = "exec:step_failed"
    EXEC_LOOP_RESTARTED = "exec:loop_restarted"
    EXEC_TASK_ATTEMPT_CAP = "exec:task_attempt_cap"


INFRA_KINDS = frozenset(k for k in vars(Kind).values() if isinstance(k, str) and k.startswith("infra:"))

AGENT_STEPS = frozenset({"implement", "fix-tests", "fix-merge"})

_AUTH_RE = re.compile(
    r"authentication_error|oauth token|not logged in|invalid api key|invalid x-api-key"
    r"|\b401\b|credit balance|please run /login|token (has )?expired",
    re.I,
)
_EXTRACTION_RE = re.compile(r"branch \S+ does not exist|worktree \S+ does not exist", re.I)
_IMAGE_HALT_RE = re.compile(r"image build|docker build|failed to build|container crash|unexpected exit|stuck", re.I)
_CONSECUTIVE_RE = re.compile(r"consecutive", re.I)
_BARE_MARKER_RE = re.compile(r"^\s*CLOCHE_RESULT:[A-Za-z][\w-]*\s*$", re.M)
_NONCED_MARKER_RE = re.compile(r"^\s*CLOCHE_RESULT:[0-9a-f]{6,}:[A-Za-z][\w-]*\s*$", re.M)
_SLOTS_RE = re.compile(r"Slots:\s*(\d+)/(\d+)\s*busy\s*[·,]\s*(\d+)\s*queued")
_LOOP_RE = re.compile(r"Orchestration loop:\s*(\w+)")
_HALT_RE = re.compile(r"(?:halt|halted|stopped)[^\n]*?(?:error|because|due to)[^\n]*", re.I)


FAILURE_RESULTS = frozenset({"fail", "failed", "timeout", "give-up", "token-limit", "error", "env-error"})


def is_failure_result(result) -> bool:
    """Step results are workflow-defined names; only the conventional
    failure names count. An intent-scan's `reconcile -> none` (nothing to
    reconcile) or a skip wire is not a failure."""
    return (result or "").lower() in FAILURE_RESULTS


def parse_status(text: str) -> dict:
    """Scrape the loop state, slot usage and any halt message out of
    `cloche status` run in the project directory."""
    text = text or ""
    loop = None
    m = _LOOP_RE.search(text)
    if m:
        loop = m.group(1).lower()
    busy = maximum = queued = None
    m = _SLOTS_RE.search(text)
    if m:
        busy, maximum, queued = (int(x) for x in m.groups())
    halt = None
    m = _HALT_RE.search(text)
    if m:
        halt = m.group(0).strip()
    return {"loop": loop, "busy": busy, "max": maximum, "queued": queued, "halt": halt}


def classify_halt(halt_message: str) -> str:
    """A loop the daemon stopped on its own is an execution outcome when it
    tripped the consecutive-failure ceiling, and infrastructure otherwise."""
    if halt_message and _CONSECUTIVE_RE.search(halt_message) and not _IMAGE_HALT_RE.search(halt_message):
        return Kind.EXEC_LOOP_RESTARTED
    return Kind.INFRA_LOOP_HALTED


def classify_step_failure(step: str, log_text: str) -> str:
    """Classify a failed step from its log."""
    log_text = log_text or ""
    if step == "image-build":
        return Kind.INFRA_IMAGE
    if _AUTH_RE.search(log_text):
        return Kind.INFRA_AUTH
    if step == "merge" and _EXTRACTION_RE.search(log_text):
        return Kind.INFRA_EXTRACTION
    if step in AGENT_STEPS and _BARE_MARKER_RE.search(log_text) and not _NONCED_MARKER_RE.search(log_text):
        return Kind.EXEC_MARKER_DROP
    return Kind.EXEC_STEP_FAILED


def failed_attempts_per_task(entries) -> dict:
    counts = {}
    for entry in entries:
        if entry.get("kind") == "attempt_ended" and entry.get("task_id"):
            if entry.get("state") != "succeeded":
                counts[entry["task_id"]] = counts.get(entry["task_id"], 0) + 1
    return counts


class InfraMonitor:
    """Stateful per-arm monitor. `check(entries)` consumes the activity log
    incrementally and returns the abort kind (an `infra:*` or the attempt
    cap) or None. Every observation is appended to `self.events`."""

    def __init__(self, tc, clock=time.monotonic, poll_interval_seconds: float = 5.0,
                 stall_seconds: float = 2700.0, stale_slot_seconds: float = 120.0,
                 max_loop_restarts: int = 1, max_attempts_per_task=None,
                 daemon_log_path: str = "/tmp/cloched.log"):
        self.tc = tc
        self.clock = clock
        self.poll_interval_seconds = poll_interval_seconds
        self.stall_seconds = stall_seconds
        self.stale_slot_seconds = stale_slot_seconds
        self.max_loop_restarts = max_loop_restarts
        self.max_attempts_per_task = max_attempts_per_task
        self.daemon_log_path = daemon_log_path
        self.events = []
        self.processed = 0
        self.last_progress = clock()
        self.stale_since = None
        self.loop_restarts = 0
        self.expect_running = True

    # -- bookkeeping ---------------------------------------------------------

    def _event(self, kind, detail, **extra):
        event = {"kind": kind, "detail": detail, "t": self.clock(), **extra}
        self.events.append(event)
        return event

    def _abort(self, kind, detail, **extra):
        evidence = None
        tail = getattr(self.tc, "daemon_log_tail", None)
        if tail is not None:
            try:
                evidence = tail(self.daemon_log_path, 120)
            except Exception as exc:  # evidence is best-effort
                evidence = f"(daemon log unavailable: {exc})"
        self._event(kind, detail, abort=True, daemon_log=evidence, **extra)
        return kind

    # -- checks --------------------------------------------------------------

    def check(self, entries) -> str:
        health = getattr(self.tc, "health_ok", None)
        if health is not None and not health():
            return self._abort(Kind.INFRA_DAEMON, "cloche health failed (daemon unreachable)")

        kind = self._check_new_entries(entries)
        if kind:
            return kind

        # A store the daemon cannot resolve means every subsequent step runs
        # with no injection at all — an arm B that has silently become arm A
        # (x3 arm-b-r1 and x4 arm-b-r3, cloche-etr2). Abort rather than
        # collect a replication that measures nothing.
        store_err = getattr(self.tc, "intent_store_error", None)
        if store_err is not None:
            err = store_err()
            if err:
                return self._abort(Kind.INFRA_INTENT_STORE, f"intent store unresolvable: {err}")

        kind = self._check_attempt_cap(entries)
        if kind:
            return kind

        status = parse_status(self._status_text())
        kind = self._check_loop_state(status)
        if kind:
            return kind

        kind = self._check_stale_slot(status)
        if kind:
            return kind

        return self._check_stall()

    def _status_text(self):
        fn = getattr(self.tc, "project_status_text", None)
        return fn() if fn is not None else ""

    def _check_new_entries(self, entries):
        new = entries[self.processed:]
        if new:
            self.last_progress = self.clock()
        self.processed = len(entries)
        for entry in new:
            if entry.get("kind") != "step_completed" or not is_failure_result(entry.get("result")):
                continue
            step = entry.get("step") or ""
            task_id = entry.get("task_id") or ""
            log_text = ""
            step_log = getattr(self.tc, "step_log", None)
            if step_log is not None and task_id:
                try:
                    log_text = step_log(task_id, step) or ""
                except Exception as exc:
                    log_text = f"(log unavailable: {exc})"
            kind = classify_step_failure(step, log_text)
            detail = f"step {step!r} of {task_id} ended with {entry.get('result')!r}"
            if kind in INFRA_KINDS:
                return self._abort(kind, detail, task_id=task_id, step=step, log_tail=log_text[-2000:])
            self._event(kind, detail, task_id=task_id, step=step)
        return None

    def _check_attempt_cap(self, entries):
        if not self.max_attempts_per_task:
            return None
        for task_id, failed in failed_attempts_per_task(entries).items():
            if failed >= self.max_attempts_per_task:
                self._event(Kind.EXEC_TASK_ATTEMPT_CAP,
                            f"{task_id} failed {failed} attempts (cap {self.max_attempts_per_task})",
                            abort=True, task_id=task_id)
                return Kind.EXEC_TASK_ATTEMPT_CAP
        return None

    def _check_loop_state(self, status):
        if status["loop"] != "stopped" or not self.expect_running:
            return None
        kind = classify_halt(status["halt"] or "")
        detail = f"loop stopped by daemon: {status['halt'] or '(no halt message)'}"
        if kind == Kind.INFRA_LOOP_HALTED:
            return self._abort(kind, detail)
        return self._restart_loop(kind, detail)

    def _check_stale_slot(self, status):
        busy, queued = status["busy"], status["queued"]
        if busy is None or queued is None:
            return None
        if queued > 0 and busy == 0:
            now = self.clock()
            if self.stale_since is None:
                self.stale_since = now
                return None
            if now - self.stale_since >= self.stale_slot_seconds:
                self.stale_since = None
                return self._restart_loop(
                    Kind.INFRA_STALE_SLOT,
                    f"{queued} queued for capacity with 0/{status['max']} busy for {self.stale_slot_seconds:.0f}s",
                )
            return None
        self.stale_since = None
        return None

    def _restart_loop(self, kind, detail):
        if self.loop_restarts >= self.max_loop_restarts:
            return self._abort(kind, detail + f" (restart budget {self.max_loop_restarts} exhausted)")
        self.loop_restarts += 1
        self._event(kind, detail + " — restarting loop", restart=self.loop_restarts)
        try:
            self.tc.loop_stop()
        except Exception:
            pass
        self.tc.loop_start()
        self.last_progress = self.clock()
        return None

    def _check_stall(self):
        if self.clock() - self.last_progress >= self.stall_seconds:
            return self._abort(Kind.INFRA_STALL, f"no activity for {self.stall_seconds:.0f}s")
        return None
