import os
import tempfile
import unittest

from agent_command.edits import (
    UnsafePathError,
    apply_edits,
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

    def test_writes_file_and_adds_trailing_newline(self):
        blocks = parse_edit_blocks("```calc.py\ndef f():\n    pass\n```")
        written = apply_edits(self.tmp, blocks)
        self.assertEqual(written, ["calc.py"])
        with open(os.path.join(self.tmp, "calc.py")) as f:
            self.assertEqual(f.read(), "def f():\n    pass\n")

    def test_creates_parent_dirs(self):
        blocks = parse_edit_blocks("```sub/dir/new.py\nx = 1\n```")
        apply_edits(self.tmp, blocks)
        self.assertTrue(os.path.exists(os.path.join(self.tmp, "sub", "dir", "new.py")))

    def test_unsafe_path_blocks_all_writes(self):
        text = "```good.py\nsafe content\n```\n```../bad.py\nescape content\n```"
        blocks = parse_edit_blocks(text)
        with self.assertRaises(UnsafePathError):
            apply_edits(self.tmp, blocks)
        self.assertFalse(os.path.exists(os.path.join(self.tmp, "good.py")))


if __name__ == "__main__":
    unittest.main()
