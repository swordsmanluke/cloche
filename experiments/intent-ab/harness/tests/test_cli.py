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

    def test_inlines_design_doc_when_present(self):
        with open(os.path.join(self.tmp, "DESIGN.md"), "w") as f:
            f.write("## Section 10: Standing Constraints\nNo stray print().\n")
        prompt = build_system_prompt(self.tmp)
        self.assertIn("Standing Constraints", prompt)
        self.assertIn("No stray print().", prompt)

    def test_no_design_doc_section_when_absent(self):
        # Must degrade quietly (no "DESIGN.md" file exists in the task's
        # workdir) rather than crash or claim there's a spec to follow.
        prompt = build_system_prompt(self.tmp)
        self.assertNotIn("full specification", prompt)


if __name__ == "__main__":
    unittest.main()
