import os
import tempfile
import unittest

from agent_command.edits import (
    EXAMPLE_PLACEHOLDER_PATH,
    NoExistingFileOverlapError,
    PlaceholderPathError,
    UnsafePathError,
    apply_edits,
    list_workdir_files,
    parse_edit_blocks,
    resolve_safe_path,
)


class TestParseEditBlocks(unittest.TestCase):
    def test_single_block(self):
        text = "```calc.py\ndef add(a, b):\n    return a + b\n```"
        blocks = parse_edit_blocks(text)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].path, "calc.py")
        self.assertEqual(blocks[0].content, "def add(a, b):\n    return a + b")

    def test_multiple_blocks(self):
        text = (
            "some prose\n"
            "```a/one.py\ncontent one\n```\n"
            "more prose\n"
            "```b/two.py\ncontent two\n```\n"
        )
        blocks = parse_edit_blocks(text)
        self.assertEqual([b.path for b in blocks], ["a/one.py", "b/two.py"])
        self.assertEqual(blocks[0].content, "content one")
        self.assertEqual(blocks[1].content, "content two")

    def test_ignores_language_only_fence(self):
        text = "```python\nprint('not a file path')\n```"
        self.assertEqual(parse_edit_blocks(text), [])

    def test_no_blocks(self):
        self.assertEqual(parse_edit_blocks("no fences here"), [])

    def test_new_marker_sets_flag_and_strips_from_path(self):
        text = "```new/module.py NEW\ncontent\n```"
        blocks = parse_edit_blocks(text)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(blocks[0].path, "new/module.py")
        self.assertTrue(blocks[0].is_new)

    def test_new_marker_is_case_insensitive(self):
        blocks = parse_edit_blocks("```new/module.py new\ncontent\n```")
        self.assertEqual(blocks[0].path, "new/module.py")
        self.assertTrue(blocks[0].is_new)

    def test_no_new_marker_defaults_flag_false(self):
        blocks = parse_edit_blocks("```existing.py\ncontent\n```")
        self.assertFalse(blocks[0].is_new)


class TestResolveSafePath(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def test_plain_relative_path(self):
        resolved = resolve_safe_path(self.tmp, "calc.py")
        self.assertEqual(resolved, os.path.join(os.path.realpath(self.tmp), "calc.py"))

    def test_nested_relative_path(self):
        resolved = resolve_safe_path(self.tmp, "sub/dir/file.py")
        self.assertTrue(resolved.startswith(os.path.realpath(self.tmp)))

    def test_rejects_parent_escape(self):
        with self.assertRaises(UnsafePathError):
            resolve_safe_path(self.tmp, "../escape.py")

    def test_rejects_absolute_path(self):
        with self.assertRaises(UnsafePathError):
            resolve_safe_path(self.tmp, "/etc/passwd")


class TestApplyEdits(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def _touch(self, rel_path, content=""):
        path = os.path.join(self.tmp, rel_path)
        os.makedirs(os.path.dirname(path) or self.tmp, exist_ok=True)
        with open(path, "w") as f:
            f.write(content)

    def test_writes_file_and_adds_trailing_newline(self):
        self._touch("calc.py", "def f():\n    return None\n")
        blocks = parse_edit_blocks("```calc.py\ndef f():\n    pass\n```")
        written = apply_edits(self.tmp, blocks)
        self.assertEqual(written, ["calc.py"])
        with open(os.path.join(self.tmp, "calc.py")) as f:
            self.assertEqual(f.read(), "def f():\n    pass\n")

    def test_creates_parent_dirs_for_new_file_when_marked(self):
        blocks = parse_edit_blocks("```sub/dir/new.py NEW\nx = 1\n```")
        apply_edits(self.tmp, blocks)
        self.assertTrue(os.path.exists(os.path.join(self.tmp, "sub", "dir", "new.py")))

    def test_unsafe_path_blocks_all_writes(self):
        self._touch("good.py", "safe\n")
        text = "```good.py\nsafe content\n```\n```../bad.py\nescape content\n```"
        blocks = parse_edit_blocks(text)
        with self.assertRaises(UnsafePathError):
            apply_edits(self.tmp, blocks)
        with open(os.path.join(self.tmp, "good.py")) as f:
            self.assertEqual(f.read(), "safe\n")

    def test_rejects_placeholder_path(self):
        blocks = parse_edit_blocks(f"```{EXAMPLE_PLACEHOLDER_PATH}\nsome content\n```")
        with self.assertRaises(PlaceholderPathError):
            apply_edits(self.tmp, blocks)
        self.assertEqual(os.listdir(self.tmp), [])

    def test_rejects_placeholder_path_even_if_nested(self):
        # A model that copies the example verbatim but prefixes it with a
        # directory should still be caught (the ticket's "relative/path/to/
        # greet.py" case: a plausible-looking path built around the
        # placeholder rather than an exact match of it).
        blocks = parse_edit_blocks(f"```some/dir/{EXAMPLE_PLACEHOLDER_PATH}\nsome content\n```")
        with self.assertRaises(PlaceholderPathError):
            apply_edits(self.tmp, blocks)

    def test_fails_when_no_applied_path_overlaps_existing_files(self):
        self._touch("greet.py", "original\n")
        blocks = parse_edit_blocks("```relative/path/to/greet.py\nfixed content\n```")
        with self.assertRaises(NoExistingFileOverlapError):
            apply_edits(self.tmp, blocks)
        with open(os.path.join(self.tmp, "greet.py")) as f:
            self.assertEqual(f.read(), "original\n")
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "relative", "path", "to", "greet.py")))

    def test_succeeds_when_marked_new_despite_no_overlap(self):
        blocks = parse_edit_blocks("```brand/new/file.py NEW\ncontent\n```")
        written = apply_edits(self.tmp, blocks)
        self.assertEqual(written, ["brand/new/file.py"])

    def test_succeeds_when_at_least_one_block_overlaps_existing_file(self):
        self._touch("existing.py", "old\n")
        text = "```existing.py\nnew content\n```\n```brand/new/file.py NEW\nmore content\n```"
        blocks = parse_edit_blocks(text)
        written = apply_edits(self.tmp, blocks)
        self.assertEqual(written, ["existing.py", "brand/new/file.py"])

    def test_no_blocks_does_not_raise(self):
        self.assertEqual(apply_edits(self.tmp, []), [])


class TestListWorkdirFiles(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def test_lists_files_relative_to_workdir(self):
        os.makedirs(os.path.join(self.tmp, "sub"))
        for rel in ("a.py", "sub/b.py"):
            with open(os.path.join(self.tmp, rel), "w") as f:
                f.write("x")
        self.assertEqual(list_workdir_files(self.tmp), ["a.py", "sub/b.py"])

    def test_skips_git_metadata(self):
        os.makedirs(os.path.join(self.tmp, ".git"))
        with open(os.path.join(self.tmp, ".git", "config"), "w") as f:
            f.write("x")
        with open(os.path.join(self.tmp, "a.py"), "w") as f:
            f.write("x")
        self.assertEqual(list_workdir_files(self.tmp), ["a.py"])

    def test_empty_workdir(self):
        self.assertEqual(list_workdir_files(self.tmp), [])


if __name__ == "__main__":
    unittest.main()
