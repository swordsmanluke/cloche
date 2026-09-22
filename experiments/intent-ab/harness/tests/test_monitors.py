"""Unit tests for arm_driver.monitors: the infra-vs-execution classification
the replication driver relies on, driven by a fake toolchain and clock."""
import unittest

from arm_driver.monitors import (InfraMonitor, Kind, classify_halt, classify_step_failure,
                                 failed_attempts_per_task, parse_status)


class FakeTC:
    def __init__(self, status="Orchestration loop: running\nSlots: 1/1 busy · 0 queued\n",
                 logs=None, healthy=True):
        self.status = status
        self.logs = logs or {}
        self.healthy = healthy
        self.starts = 0
        self.stops = 0

    def health_ok(self):
        return self.healthy

    def project_status_text(self):
        return self.status

    def step_log(self, task_id, step, log_type="full", limit=400):
        return self.logs.get((task_id, step), "")

    def daemon_log_tail(self, path, lines=120):
        return "daemon log evidence"

    def loop_start(self):
        self.starts += 1

    def loop_stop(self):
        self.stops += 1


class FakeClock:
    def __init__(self):
        self.t = 0.0

    def __call__(self):
        return self.t

    def advance(self, s):
        self.t += s


def step_failed(task, step, result="fail"):
    return {"kind": "step_completed", "task_id": task, "step": step, "result": result}


def attempt_ended(task, state):
    return {"kind": "attempt_ended", "task_id": task, "state": state}


class TestPureClassifiers(unittest.TestCase):
    def test_parse_status(self):
        s = parse_status("Project: arm-a\nOrchestration loop: stopped\nSlots: 0/1 busy · 1 queued\n")
        self.assertEqual((s["loop"], s["busy"], s["max"], s["queued"]), ("stopped", 0, 1, 1))
        self.assertEqual(parse_status("")["loop"], None)

    def test_halt_classification(self):
        self.assertEqual(classify_halt("loop halted: 3 consecutive failures"), Kind.EXEC_LOOP_RESTARTED)
        self.assertEqual(classify_halt("loop halted: image build failed"), Kind.INFRA_LOOP_HALTED)
        self.assertEqual(classify_halt(""), Kind.INFRA_LOOP_HALTED)

    def test_step_failure_classification(self):
        self.assertEqual(classify_step_failure("image-build", "boom"), Kind.INFRA_IMAGE)
        self.assertEqual(classify_step_failure("implement", "authentication_error: OAuth token expired"),
                         Kind.INFRA_AUTH)
        self.assertEqual(classify_step_failure("merge", "error: branch cloche/abcd-develop does not exist"),
                         Kind.INFRA_EXTRACTION)
        self.assertEqual(classify_step_failure("implement", "did the work\nCLOCHE_RESULT:success\n"),
                         Kind.EXEC_MARKER_DROP)
        self.assertEqual(classify_step_failure("implement", "CLOCHE_RESULT:9ae5072ae2de:fail\n"),
                         Kind.EXEC_STEP_FAILED)
        self.assertEqual(classify_step_failure("test", "FAILED (errors=2)"), Kind.EXEC_STEP_FAILED)

    def test_failed_attempts_per_task(self):
        entries = [attempt_ended("t1", "failed"), attempt_ended("t1", "failed"),
                   attempt_ended("t2", "succeeded"), attempt_ended("t1", "succeeded")]
        self.assertEqual(failed_attempts_per_task(entries), {"t1": 2})


class TestInfraMonitor(unittest.TestCase):
    def make(self, tc, **kw):
        clock = FakeClock()
        m = InfraMonitor(tc, clock=clock, poll_interval_seconds=5, stall_seconds=100,
                         stale_slot_seconds=20, max_loop_restarts=1, max_attempts_per_task=3, **kw)
        return m, clock

    def test_healthy_idle_run_returns_none(self):
        m, _ = self.make(FakeTC())
        self.assertIsNone(m.check([]))
        self.assertEqual(m.events, [])

    def test_daemon_down_aborts(self):
        m, _ = self.make(FakeTC(healthy=False))
        self.assertEqual(m.check([]), Kind.INFRA_DAEMON)
        self.assertEqual(m.events[-1]["daemon_log"], "daemon log evidence")

    def test_extraction_branch_missing_aborts_with_evidence(self):
        tc = FakeTC(logs={("t1", "merge"): "error: branch cloche/x-develop does not exist"})
        m, _ = self.make(tc)
        self.assertEqual(m.check([step_failed("t1", "merge")]), Kind.INFRA_EXTRACTION)
        self.assertTrue(m.events[-1]["abort"])
        self.assertIn("does not exist", m.events[-1]["log_tail"])

    def test_marker_drop_is_recorded_not_aborted(self):
        tc = FakeTC(logs={("t1", "implement"): "work\nCLOCHE_RESULT:success\n"})
        m, _ = self.make(tc)
        entries = [step_failed("t1", "implement")]
        self.assertIsNone(m.check(entries))
        self.assertEqual([e["kind"] for e in m.events], [Kind.EXEC_MARKER_DROP])
        # Already-processed entries are not re-classified on the next tick.
        self.assertIsNone(m.check(entries))
        self.assertEqual(len(m.events), 1)

    def test_attempt_cap_is_an_execution_stop(self):
        m, _ = self.make(FakeTC())
        entries = [attempt_ended("t1", "failed")] * 3
        self.assertEqual(m.check(entries), Kind.EXEC_TASK_ATTEMPT_CAP)
        self.assertNotIn("infra", m.events[-1]["kind"])

    def test_stale_slot_restarts_once_then_aborts(self):
        tc = FakeTC(status="Orchestration loop: running\nSlots: 0/1 busy · 1 queued\n")
        m, clock = self.make(tc)
        self.assertIsNone(m.check([]))          # first sighting: start the timer
        clock.advance(25)
        self.assertIsNone(m.check([]))          # past threshold: one restart
        self.assertEqual((tc.stops, tc.starts), (1, 1))
        self.assertEqual(m.events[-1]["kind"], Kind.INFRA_STALE_SLOT)
        self.assertFalse(m.events[-1].get("abort"))
        clock.advance(1)
        self.assertIsNone(m.check([]))          # timer restarted
        clock.advance(25)
        self.assertEqual(m.check([]), Kind.INFRA_STALE_SLOT)   # budget exhausted: abort

    def test_daemon_halt_for_infra_reason_aborts(self):
        tc = FakeTC(status="Orchestration loop: stopped\nloop halted due to image build failure\n")
        m, _ = self.make(tc)
        self.assertEqual(m.check([]), Kind.INFRA_LOOP_HALTED)

    def test_daemon_halt_for_consecutive_failures_restarts_loop(self):
        tc = FakeTC(status="Orchestration loop: stopped\nloop halted because of 3 consecutive failures\n")
        m, _ = self.make(tc)
        self.assertIsNone(m.check([]))
        self.assertEqual(tc.starts, 1)
        self.assertEqual(m.events[-1]["kind"], Kind.EXEC_LOOP_RESTARTED)

    def test_stall_aborts_after_no_activity(self):
        m, clock = self.make(FakeTC())
        self.assertIsNone(m.check([{"kind": "attempt_started", "task_id": "t1"}]))
        clock.advance(99)
        self.assertIsNone(m.check([{"kind": "attempt_started", "task_id": "t1"}]))
        clock.advance(2)
        self.assertEqual(m.check([{"kind": "attempt_started", "task_id": "t1"}]), Kind.INFRA_STALL)


if __name__ == "__main__":
    unittest.main()
