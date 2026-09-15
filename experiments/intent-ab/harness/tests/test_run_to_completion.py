"""Unit tests for run_arm.run_to_completion's polling/cap logic, decoupled
from real subprocesses via a fake Toolchain-shaped object. The full
subprocess path (real Toolchain against fake `cloche`/`bd` executables) is
covered by test_driver_integration.py."""
import shutil
import tempfile
import unittest
from pathlib import Path

from arm_driver.contamination import ContaminationError
from arm_driver.run_arm import StopReason, run_to_completion


class FakeToolchain:
    def __init__(self, ready_sequence, attempts_sequence=None):
        self.ready_sequence = list(ready_sequence)
        self.attempts_sequence = list(attempts_sequence or [])
        self.started = False
        self.stopped = False
        self.ready_calls = 0

    def loop_start(self):
        self.started = True

    def loop_stop(self):
        self.stopped = True

    def bd_ready(self):
        self.ready_calls += 1
        if self.ready_sequence:
            return self.ready_sequence.pop(0)
        return []

    def activity_json(self):
        if self.attempts_sequence:
            n = self.attempts_sequence.pop(0)
        else:
            n = 0
        return [{"kind": "attempt_started", "task_id": f"t{i}"} for i in range(n)]


class FakeClock:
    """A clock that advances by a fixed step every time it's read, so the
    wall-cap branch is exercised without any real sleeping."""
    def __init__(self, step=1.0):
        self.value = 0.0
        self.step = step

    def __call__(self):
        self.value += self.step
        return self.value


class TestRunToCompletion(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        (self.tmp / ".cloche").mkdir()

    def test_stops_on_exhaustion(self):
        tc = FakeToolchain(ready_sequence=[[{"id": "t1"}], [{"id": "t1"}], []])
        result = run_to_completion(
            tc, "b", self.tmp, wall_cap_seconds=1000,
            sleep=lambda _: None, clock=FakeClock(step=0.01),
        )
        self.assertEqual(result["stop_reason"], StopReason.EXHAUSTED)
        self.assertTrue(tc.started)
        self.assertTrue(tc.stopped)
        self.assertEqual(tc.ready_calls, 3)

    def test_stops_on_wall_cap(self):
        # bd_ready never empties out on its own; only the wall cap ends it.
        class NeverExhausted(FakeToolchain):
            def bd_ready(self):
                self.ready_calls += 1
                return [{"id": "t1"}]

        tc = NeverExhausted(ready_sequence=[])
        result = run_to_completion(
            tc, "b", self.tmp, wall_cap_seconds=0.05,
            sleep=lambda _: None, clock=FakeClock(step=0.03),
        )
        self.assertEqual(result["stop_reason"], StopReason.WALL_CAP)
        self.assertTrue(tc.stopped)

    def test_stops_on_attempt_budget_cap(self):
        class AlwaysReady(FakeToolchain):
            def bd_ready(self):
                self.ready_calls += 1
                return [{"id": "t1"}]

        tc = AlwaysReady(ready_sequence=[], attempts_sequence=[1, 2, 3])
        result = run_to_completion(
            tc, "b", self.tmp, wall_cap_seconds=1000, max_attempts=3,
            sleep=lambda _: None, clock=FakeClock(step=0.01),
        )
        self.assertEqual(result["stop_reason"], StopReason.ATTEMPT_CAP)
        self.assertTrue(tc.stopped)

    def test_loop_is_stopped_even_when_a_contamination_check_raises(self):
        # Contamination appears as a *side effect* of the first bd_ready()
        # call (simulating it turning up mid-run), so the loop has already
        # started by the time the next poll tick's assert_clean() catches
        # it -- exercising the `finally` block, not the pre-start check.
        tmp = self.tmp

        class ContaminatesOnFirstPoll(FakeToolchain):
            def bd_ready(self):
                self.ready_calls += 1
                if self.ready_calls == 1:
                    (tmp / ".cloche" / "intent").mkdir()
                return [{"id": "t1"}]

        tc = ContaminatesOnFirstPoll(ready_sequence=[])
        with self.assertRaises(ContaminationError):
            run_to_completion(
                tc, "a", self.tmp, wall_cap_seconds=1000,
                sleep=lambda _: None, clock=FakeClock(step=0.01),
            )
        self.assertTrue(tc.started)
        self.assertTrue(tc.stopped)  # the `finally` block must still run


if __name__ == "__main__":
    unittest.main()
