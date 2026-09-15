import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from arm_driver.seed import SeedCloneError, clone_seed


def _git(args, cwd):
    env = {
        "GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
        "GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
    }
    import os
    full_env = dict(os.environ)
    full_env.update(env)
    subprocess.run(["git", *args], cwd=str(cwd), check=True, env=full_env, capture_output=True)


class TestCloneSeedFromSubtree(unittest.TestCase):
    """Mirrors the seed's actual situation: a subdirectory of a larger
    monorepo, frozen at a tag, extracted into a standalone tree."""

    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

        self.monorepo = self.tmp / "monorepo"
        self.monorepo.mkdir()
        _git(["init", "-q"], self.monorepo)
        (self.monorepo / "unrelated.txt").write_text("not part of the seed\n")
        seed = self.monorepo / "experiments" / "seed"
        seed.mkdir(parents=True)
        (seed / "README.md").write_text("seed content\n")
        (seed / ".cloche").mkdir()
        (seed / ".cloche" / "config.toml").write_text("active = true\n")
        _git(["add", "-A"], self.monorepo)
        _git(["commit", "-q", "-m", "seed v1"], self.monorepo)
        _git(["tag", "seed-v1"], self.monorepo)

        # A later commit that must NOT show up in the tagged extraction.
        (seed / "README.md").write_text("seed content, drifted after freeze\n")
        _git(["add", "-A"], self.monorepo)
        _git(["commit", "-q", "-m", "drift after freeze"], self.monorepo)

        self.seed_source = seed
        self.target = self.tmp / "arm-a-checkout"

    def test_extracts_tagged_content_not_head(self):
        clone_seed(self.seed_source, "seed-v1", self.target)

        self.assertEqual((self.target / "README.md").read_text(), "seed content\n")
        self.assertEqual((self.target / ".cloche" / "config.toml").read_text(), "active = true\n")
        # The rest of the monorepo (outside the seed subtree) must not leak in.
        self.assertFalse((self.target / "unrelated.txt").exists())
        self.assertFalse((self.target / "experiments").exists())

    def test_target_is_a_fresh_git_repo(self):
        clone_seed(self.seed_source, "seed-v1", self.target)

        result = subprocess.run(
            ["git", "-C", str(self.target), "log", "--oneline"],
            capture_output=True, text=True, check=True,
        )
        self.assertEqual(len(result.stdout.strip().splitlines()), 1)

    def test_raises_if_target_exists(self):
        self.target.mkdir()
        with self.assertRaises(SeedCloneError):
            clone_seed(self.seed_source, "seed-v1", self.target)

    def test_raises_on_unknown_ref(self):
        with self.assertRaises(SeedCloneError):
            clone_seed(self.seed_source, "no-such-tag", self.target)


if __name__ == "__main__":
    unittest.main()
