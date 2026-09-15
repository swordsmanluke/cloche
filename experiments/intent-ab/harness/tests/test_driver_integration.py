"""End-to-end smoke test for the arm driver (E5's acceptance bar: "an
end-to-end smoke run of both arms against a 2-task stub list").

Real `git` is used for the seed extraction (arm_driver.seed.clone_seed); a
throwaway fixture repo stands in for the seed (../arms/README.md/E1's real
seed-v1 tag doesn't get touched by this test — this test never mutates the
actual monorepo). `cloche` and `bd` are faked (tests/fake_bin/, driven by
tests/fake_driver_lib.py) since no real daemon/Docker/bd is available in
this environment — the same tradeoff E4 made faking Ollama (see
harness/README.md). Every subprocess call this test does *not* fake
(overlay file composition, the real `bin/agent_command` wiring) runs for
real, so the parts of the pipeline this sandbox *can* exercise, are
exercised against real code, not mocked out.
"""
import functools
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from arm_driver.cloche_cli import Toolchain
from arm_driver.run_arm import StopReason, run_arm

HARNESS_ROOT = Path(__file__).resolve().parent.parent
FAKE_BIN = Path(__file__).resolve().parent / "fake_bin"
STUB_TASKS = HARNESS_ROOT / "arms" / "stub-tasks.json"


def _git(args, cwd, env):
    subprocess.run(["git", *args], cwd=str(cwd), check=True, env=env, capture_output=True)


def _build_fixture_seed(root: Path) -> Path:
    """A minimal seed-shaped repo, tagged, for clone_seed to extract from.
    Deliberately does NOT match the real seed byte-for-byte — the point is
    to prove the driver's overlay composition and orchestration, which
    E1/E2/E4 already cover for the real seed content itself."""
    env = dict(os.environ)
    env.update({
        "GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
        "GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
    })
    repo = root / "fixture-monorepo"
    seed = repo / "experiments" / "seed"
    seed.mkdir(parents=True)
    (seed / "README.md").write_text("fixture seed\n")
    (seed / ".cloche").mkdir()
    (seed / ".cloche" / "config.toml").write_text("active = true\n[daemon]\nimage = \"placeholder\"\n")
    (seed / ".cloche" / "develop.cloche").write_text("workflow develop {}\n")
    (seed / ".cloche" / "Dockerfile").write_text("FROM cloche-agent:latest\n")

    _git(["init", "-q"], repo, env)
    _git(["add", "-A"], repo, env)
    _git(["commit", "-q", "-m", "fixture seed"], repo, env)
    _git(["tag", "fixture-v1"], repo, env)
    return seed


class TestArmDriverSmoke(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.seed_source = _build_fixture_seed(self.tmp)
        self.toolchain_factory = functools.partial(
            Toolchain, extra_env={"PATH": f"{FAKE_BIN}{os.pathsep}{os.environ['PATH']}"},
        )

    def _run(self, arm):
        return run_arm(
            arm,
            self.tmp / f"out-{arm}",
            task_list=STUB_TASKS,
            seed_source=self.seed_source,
            ref="fixture-v1",
            toolchain_factory=self.toolchain_factory,
            poll_interval_seconds=0.0,
            wall_cap_seconds=30.0,
        )

    def test_arm_a_runs_to_exhaustion_with_no_intent_contamination(self):
        report = self._run("a")

        self.assertEqual(report["stop_reason"], StopReason.EXHAUSTED)
        metrics = report["metrics"]
        self.assertEqual(metrics["tasks_attempted"], 2)
        self.assertEqual(metrics["tasks_succeeded"], 2)
        self.assertEqual(metrics["merges_succeeded"], 2)
        self.assertEqual(metrics["fix_loop_iterations"], 0)
        self.assertNotIn("context_composition", metrics)

        target = Path(report["target_dir"])
        self.assertFalse((target / ".cloche" / "intent").exists())
        config = (target / ".cloche" / "config.toml").read_text()
        self.assertIn("scan_after_tasks = false", config)

    def test_arm_b_runs_to_exhaustion_and_reports_context_composition(self):
        report = self._run("b")

        self.assertEqual(report["stop_reason"], StopReason.EXHAUSTED)
        metrics = report["metrics"]
        self.assertEqual(metrics["tasks_attempted"], 2)
        self.assertEqual(metrics["tasks_succeeded"], 2)

        composition = metrics["context_composition"]
        self.assertIn("implement", composition)
        self.assertIn("fix-tests", composition)
        self.assertGreater(composition["implement"]["chars"], 0)

        injected = metrics["injected_requirement_ids_per_task"]
        self.assertEqual(set(injected.keys()), {"stub-01", "stub-02"})
        self.assertEqual(injected["stub-01"]["implement"], "req-aaaa")

        target = Path(report["target_dir"])
        config = (target / ".cloche" / "config.toml").read_text()
        self.assertIn("token_budget     = 1000", config)

    def test_wrapper_and_common_overlay_are_wired_into_both_arms(self):
        for arm in ("a", "b"):
            report = self._run(arm)
            target = Path(report["target_dir"])
            self.assertTrue((target / "agent_command" / "cli.py").exists())
            self.assertTrue((target / "bin" / "agent_command").exists())
            develop = (target / ".cloche" / "develop.cloche").read_text()
            self.assertIn('agent_command = "bin/agent_command"', develop)
            # The fixture's own placeholder develop.cloche must have been
            # overridden by the common/ overlay, not left in place.
            self.assertNotIn("workflow develop {}", develop)

    def test_tokens_are_collected_from_status_scrape(self):
        report = self._run("a")
        self.assertIsNotNone(report["metrics"]["tokens_total"])
        self.assertGreater(report["metrics"]["tokens_total"], 0)


if __name__ == "__main__":
    unittest.main()
