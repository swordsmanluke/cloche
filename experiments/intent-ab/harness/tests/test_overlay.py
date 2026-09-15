import shutil
import tempfile
import unittest
from pathlib import Path

from arm_driver.overlay import apply_overlay, copy_into


class TestApplyOverlay(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def test_later_overlay_wins_on_collision(self):
        base = self.tmp / "base"
        (base / ".cloche").mkdir(parents=True)
        (base / ".cloche" / "config.toml").write_text("from=base\n")
        (base / "README.md").write_text("seed readme\n")

        common = self.tmp / "common"
        (common / ".cloche").mkdir(parents=True)
        (common / ".cloche" / "develop.cloche").write_text("workflow develop {}\n")

        arm = self.tmp / "arm-a"
        (arm / ".cloche").mkdir(parents=True)
        (arm / ".cloche" / "config.toml").write_text("from=arm-a\n")

        apply_overlay(base, [common, arm])

        self.assertEqual((base / ".cloche" / "config.toml").read_text(), "from=arm-a\n")
        self.assertEqual((base / ".cloche" / "develop.cloche").read_text(), "workflow develop {}\n")
        self.assertEqual((base / "README.md").read_text(), "seed readme\n")

    def test_missing_overlay_dir_raises(self):
        base = self.tmp / "base"
        base.mkdir()
        with self.assertRaises(FileNotFoundError):
            apply_overlay(base, [self.tmp / "does-not-exist"])

    def test_pycache_is_not_copied(self):
        base = self.tmp / "base"
        base.mkdir()
        source = self.tmp / "source"
        (source / "__pycache__").mkdir(parents=True)
        (source / "__pycache__" / "mod.cpython-311.pyc").write_bytes(b"junk")
        (source / "mod.py").write_text("x = 1\n")

        apply_overlay(base, [source])

        self.assertTrue((base / "mod.py").exists())
        self.assertFalse((base / "__pycache__").exists())


class TestCopyInto(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def test_copies_source_under_named_subdirectory(self):
        target = self.tmp / "target"
        target.mkdir()
        source = self.tmp / "wrapper"
        (source / "cli.py").parent.mkdir(parents=True, exist_ok=True)
        (source / "cli.py").write_text("def main(): pass\n")

        copy_into(target, "agent_command", source)

        self.assertEqual((target / "agent_command" / "cli.py").read_text(), "def main(): pass\n")


if __name__ == "__main__":
    unittest.main()
