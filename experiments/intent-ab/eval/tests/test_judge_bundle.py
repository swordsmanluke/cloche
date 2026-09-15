"""Tests for the blinded-judging prep script (E6): both arm trees get
copied, `.cloche/` and `.git/` are stripped, labels are randomized (not
identity-revealing), and the real mapping lands in a key file kept
separate from the bundle handed to a judge.
"""
import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

AUDIT_DIR = Path(__file__).resolve().parent.parent / "audit"
sys.path.insert(0, str(AUDIT_DIR))

import judge_bundle  # noqa: E402


class TestJudgeBundle(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

        self.arm_a = self._make_arm("arm_a", marker="A")
        self.arm_b = self._make_arm("arm_b", marker="B")

    def _make_arm(self, name: str, marker: str) -> Path:
        root = self.tmp / name
        (root / ".cloche" / "runs").mkdir(parents=True)
        (root / ".cloche" / "develop.cloche").write_text(f"# {marker} workflow\n")
        (root / ".git" / "refs").mkdir(parents=True)
        (root / ".git" / "HEAD").write_text("ref: refs/heads/main\n")
        (root / "src").mkdir()
        (root / "src" / "main.py").write_text(f"# arm {marker}\n")
        return root

    def test_strips_cloche_and_git(self):
        out = self.tmp / "bundle"
        judge_bundle.build_bundle(self.arm_a, self.arm_b, out, seed=1)
        for label in ("tree_1", "tree_2"):
            self.assertFalse((out / label / ".cloche").exists())
            self.assertFalse((out / label / ".git").exists())
            self.assertTrue((out / label / "src" / "main.py").exists())

    def test_key_file_maps_labels_back_to_real_arms_and_stays_out_of_bundle(self):
        out = self.tmp / "bundle"
        result = judge_bundle.build_bundle(self.arm_a, self.arm_b, out, seed=1)
        key_path = Path(result["key_path"])

        self.assertFalse(key_path.is_relative_to(out))
        key = json.loads(key_path.read_text())
        real_arms = {v["real_arm"] for v in key["mapping"].values()}
        self.assertEqual(real_arms, {"A", "B"})

        for label, info in key["mapping"].items():
            content_marker = "A" if info["real_arm"] == "A" else "B"
            copied_content = (out / label / "src" / "main.py").read_text()
            self.assertIn(content_marker, copied_content)

    def test_bundle_contents_do_not_reveal_the_mapping(self):
        out = self.tmp / "bundle"
        judge_bundle.build_bundle(self.arm_a, self.arm_b, out, seed=1)
        for path in out.rglob("*"):
            if path.is_file():
                self.assertNotIn("arm_a", str(path).lower())
                self.assertNotIn("arm_b", str(path).lower())

    def test_seed_is_reproducible(self):
        out1 = self.tmp / "bundle1"
        out2 = self.tmp / "bundle2"
        r1 = judge_bundle.build_bundle(self.arm_a, self.arm_b, out1, seed=7)
        r2 = judge_bundle.build_bundle(self.arm_a, self.arm_b, out2, seed=7)
        key1 = json.loads(Path(r1["key_path"]).read_text())
        key2 = json.loads(Path(r2["key_path"]).read_text())
        self.assertEqual(
            {k: v["real_arm"] for k, v in key1["mapping"].items()},
            {k: v["real_arm"] for k, v in key2["mapping"].items()},
        )

    def test_refuses_to_overwrite_existing_out_dir(self):
        out = self.tmp / "bundle"
        out.mkdir()
        with self.assertRaises(FileExistsError):
            judge_bundle.build_bundle(self.arm_a, self.arm_b, out, seed=1)


if __name__ == "__main__":
    unittest.main()
