import os
import tempfile
import unittest

from agent_command.cli import build_system_prompt
from agent_command.edits import EXAMPLE_PLACEHOLDER_PATH


class TestBuildSystemPrompt(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()

    def test_lists_existing_files(self):
        with open(os.path.join(self.tmp, "greet.py"), "w") as f:
            f.write("print('hi')\n")
        prompt = build_system_prompt(self.tmp)
        self.assertIn("- greet.py", prompt)

    def test_empty_workdir_says_so_rather_than_a_fake_path(self):
        prompt = build_system_prompt(self.tmp)
        self.assertIn("working directory is empty", prompt)

    def test_example_fence_uses_the_recognized_placeholder(self):
        # The example must use edits.EXAMPLE_PLACEHOLDER_PATH specifically,
        # so apply_edits's rejection check (agent_command/edits.py) actually
        # recognizes it if a model echoes it verbatim.
        prompt = build_system_prompt(self.tmp)
        self.assertIn(f"```{EXAMPLE_PLACEHOLDER_PATH}", prompt)

    def test_instructs_using_exact_existing_path(self):
        prompt = build_system_prompt(self.tmp)
        self.assertIn("exact existing path", prompt)

    def test_documents_new_file_marker(self):
        prompt = build_system_prompt(self.tmp)
        self.assertIn("NEW", prompt)


if __name__ == "__main__":
    unittest.main()
