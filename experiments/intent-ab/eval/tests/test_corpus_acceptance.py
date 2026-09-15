"""Acceptance tests for the hidden corpus + runner (E3).

Encodes the ticket's acceptance criteria directly: the reference
implementation must pass every corpus program, and the deliberately
Lox-prior calibration implementation must score zero on the prior-trap
subset while still passing a healthy share of the general suite (showing
the trap subset actually discriminates, rather than the whole corpus being
either too easy or too hard).
"""
import json
import subprocess
import sys
import unittest
from pathlib import Path

EVAL_DIR = Path(__file__).resolve().parent.parent
RUNNER = EVAL_DIR / "runner.py"


def run_corpus(repo: Path) -> dict:
    proc = subprocess.run(
        [sys.executable, str(RUNNER), "--repo", str(repo)],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(proc.stdout)


class TestManifest(unittest.TestCase):
    def test_48_programs_with_at_least_two_per_trap(self):
        manifest = json.loads((EVAL_DIR / "corpus" / "manifest.json").read_text())
        self.assertEqual(len(manifest), 48)

        traps = {}
        for case in manifest:
            if case["trap"]:
                traps.setdefault(case["trap"], 0)
                traps[case["trap"]] += 1
        self.assertEqual(set(traps), {"Q1", "Q2", "Q3", "Q4", "Q5", "Q6", "Q7", "D3"})
        for trap, count in traps.items():
            self.assertGreaterEqual(count, 2, f"trap {trap} has only {count} program(s)")


class TestReferenceImplementation(unittest.TestCase):
    def test_reference_passes_48_of_48(self):
        report = run_corpus(EVAL_DIR / "reference")
        self.assertEqual(
            report["overall"]["passed"],
            report["overall"]["total"],
            msg=f"reference implementation did not pass every program: {report['failures']}",
        )
        self.assertEqual(report["overall"]["total"], 48)
        self.assertEqual(report["prior_trap"]["overall"]["pass_rate"], 1.0)


class TestLoxPriorCalibration(unittest.TestCase):
    def test_lox_prior_scores_zero_on_trap_subset(self):
        report = run_corpus(EVAL_DIR / "lox_prior")
        self.assertEqual(
            report["prior_trap"]["overall"]["passed"],
            0,
            msg=f"Lox-prior implementation should score ~0 on traps: {report['prior_trap']}",
        )

    def test_lox_prior_still_passes_general_coverage(self):
        report = run_corpus(EVAL_DIR / "lox_prior")
        general_total = report["overall"]["total"] - report["prior_trap"]["overall"]["total"]
        general_passed = report["overall"]["passed"] - report["prior_trap"]["overall"]["passed"]
        # It should clear a healthy share of the non-trap suite -- if it
        # didn't, the corpus wouldn't be discriminating trap-specific
        # failure from general incompetence.
        self.assertGreater(general_passed / general_total, 0.5)


if __name__ == "__main__":
    unittest.main()
