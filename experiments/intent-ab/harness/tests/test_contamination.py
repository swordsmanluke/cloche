import json
import shutil
import tempfile
import unittest
from pathlib import Path

from arm_driver.contamination import (
    ContaminationError,
    assert_no_eval_corpus,
    assert_no_intent_dir,
    corpus_filenames,
)
from arm_driver.run_arm import DEFAULT_EVAL_CORPUS_DIR, DEFAULT_SEED_SOURCE


class TestAssertNoIntentDir(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def test_passes_on_clean_tree(self):
        (self.tmp / ".cloche").mkdir()
        assert_no_intent_dir(self.tmp)  # no raise

    def test_raises_when_intent_dir_present(self):
        (self.tmp / ".cloche" / "intent").mkdir(parents=True)
        with self.assertRaises(ContaminationError):
            assert_no_intent_dir(self.tmp)

    def test_raises_for_nested_cloche_intent_dir(self):
        nested = self.tmp / ".gitworktrees_but_not_skipped" / ".cloche" / "intent"
        nested.mkdir(parents=True)
        with self.assertRaises(ContaminationError):
            assert_no_intent_dir(self.tmp)

    def test_ignores_intent_dir_inside_skipped_git_dir(self):
        # A real .git/ can contain all sorts of paths; make sure the walk
        # never mistakes something inside .git for a project .cloche/intent.
        skipped = self.tmp / ".git" / ".cloche" / "intent"
        skipped.mkdir(parents=True)
        assert_no_intent_dir(self.tmp)  # no raise


class TestAssertNoEvalCorpus(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)
        self.corpus_dir = self.tmp / "corpus"
        self.corpus_dir.mkdir()
        (self.corpus_dir / "manifest.json").write_text(json.dumps([
            {"id": "q1_if_int_condition", "file": "q1_if_int_condition.bract"},
            {"id": "q7_and_side_effect", "file": "q7_and_side_effect.bract"},
        ]))

    def test_passes_on_clean_tree(self):
        (self.tmp / "project" / "bract").mkdir(parents=True)
        assert_no_eval_corpus(self.tmp / "project", self.corpus_dir)  # no raise

    def test_raises_on_eval_directory(self):
        project = self.tmp / "project"
        (project / "eval").mkdir(parents=True)
        with self.assertRaises(ContaminationError):
            assert_no_eval_corpus(project, self.corpus_dir)

    def test_raises_on_corpus_filename_anywhere_in_tree(self):
        project = self.tmp / "project" / "some" / "nested" / "dir"
        project.mkdir(parents=True)
        (project / "q1_if_int_condition.bract").write_text("let x = 1;\n")
        with self.assertRaises(ContaminationError):
            assert_no_eval_corpus(self.tmp / "project", self.corpus_dir)

    def test_corpus_filenames_reads_manifest(self):
        names = corpus_filenames(self.corpus_dir)
        self.assertEqual(names, {"q1_if_int_condition.bract", "q7_and_side_effect.bract"})


class TestRealSeedIsClean(unittest.TestCase):
    """Regression guard: the seed repo as committed today must already be
    free of both contamination signals — if it ever isn't, arm A's own
    starting point would already violate the invariant the driver exists
    to enforce."""

    def test_seed_has_no_intent_dir(self):
        assert_no_intent_dir(DEFAULT_SEED_SOURCE)

    def test_seed_has_no_eval_corpus(self):
        assert_no_eval_corpus(DEFAULT_SEED_SOURCE, DEFAULT_EVAL_CORPUS_DIR)


if __name__ == "__main__":
    unittest.main()
