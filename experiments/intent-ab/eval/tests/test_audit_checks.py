"""Acceptance tests for the E6 audit checkers: "checkers produce correct
verdicts against hand-made compliant and violating fixture trees."

Fixtures live in ../audit/fixtures/ -- one compliant tree that should pass
every D1-D5 / SC1-SC12 check, and one violation tree per constraint (built
by ../audit/fixtures/build_violations.py) that should fail exactly that
constraint and pass every other one.

SC9 and D5's violation fixtures share the same underlying formatter bug
(see build_violations.py's _swap_binary_operands): a formatter that swaps
operands of non-commutative binary operators is both non-idempotent (D5)
and changes program behavior after a format round-trip (SC9). So SC9's
fixture is asserted to fail both SC9 and D5, not SC9 alone.
"""
import json
import subprocess
import sys
import unittest
from pathlib import Path

EVAL_DIR = Path(__file__).resolve().parent.parent
AUDIT_DIR = EVAL_DIR / "audit"
AUDIT_PY = AUDIT_DIR / "audit.py"
FIXTURES_DIR = AUDIT_DIR / "fixtures"

ALL_CHECK_IDS = {
    "D1", "D2", "D3", "D4", "D5",
    "SC1", "SC2", "SC3", "SC4", "SC5", "SC6", "SC7", "SC8", "SC9", "SC10", "SC11", "SC12",
}

# check id -> extra check ids that fixture is *also* expected to fail,
# beyond the one it's named for.
EXPECTED_EXTRA_FAILURES = {
    "D5": {"SC9"},
    "SC9": {"D5"},
}


def run_audit(repo: Path) -> dict:
    proc = subprocess.run(
        [sys.executable, str(AUDIT_PY), "--repo", str(repo)],
        capture_output=True,
        text=True,
        cwd=str(AUDIT_DIR),
    )
    return json.loads(proc.stdout)


def failed_ids(report: dict) -> set:
    all_verdicts = {**report["drift"], **report["standing"]}
    return {cid for cid, v in all_verdicts.items() if not v["passed"]}


class TestCompliantFixture(unittest.TestCase):
    def test_compliant_tree_passes_every_check(self):
        report = run_audit(FIXTURES_DIR / "compliant")
        failures = failed_ids(report)
        self.assertEqual(failures, set(), f"unexpected failures on the compliant fixture: {failures}")
        self.assertEqual(report["summary"]["total"], len(ALL_CHECK_IDS))
        self.assertEqual(report["summary"]["passed"], len(ALL_CHECK_IDS))


class TestViolationFixtures(unittest.TestCase):
    def test_every_check_id_has_a_violation_fixture(self):
        present = {p.name for p in (FIXTURES_DIR / "violations").iterdir() if p.is_dir()}
        self.assertEqual(present, ALL_CHECK_IDS)

    def test_each_violation_fixture_fails_exactly_its_own_check(self):
        for check_id in sorted(ALL_CHECK_IDS):
            with self.subTest(check_id=check_id):
                report = run_audit(FIXTURES_DIR / "violations" / check_id)
                expected = {check_id} | EXPECTED_EXTRA_FAILURES.get(check_id, set())
                failures = failed_ids(report)
                self.assertEqual(
                    failures,
                    expected,
                    f"{check_id} fixture: expected exactly {expected} to fail, got {failures}",
                )


class TestAuditCLI(unittest.TestCase):
    def test_only_flag_restricts_to_requested_checks(self):
        report = run_audit_only(FIXTURES_DIR / "compliant", "D1,SC5")
        self.assertEqual(set(report["drift"]) | set(report["standing"]), {"D1", "SC5"})

    def test_exit_code_reflects_pass_fail(self):
        proc_pass = subprocess.run(
            [sys.executable, str(AUDIT_PY), "--repo", str(FIXTURES_DIR / "compliant")],
            capture_output=True,
            cwd=str(AUDIT_DIR),
        )
        self.assertEqual(proc_pass.returncode, 0)

        proc_fail = subprocess.run(
            [sys.executable, str(AUDIT_PY), "--repo", str(FIXTURES_DIR / "violations" / "D1")],
            capture_output=True,
            cwd=str(AUDIT_DIR),
        )
        self.assertEqual(proc_fail.returncode, 1)


def run_audit_only(repo: Path, only: str) -> dict:
    proc = subprocess.run(
        [sys.executable, str(AUDIT_PY), "--repo", str(repo), "--only", only],
        capture_output=True,
        text=True,
        cwd=str(AUDIT_DIR),
    )
    return json.loads(proc.stdout)


if __name__ == "__main__":
    unittest.main()
