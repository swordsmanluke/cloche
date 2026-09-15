import unittest

from agent_command.cot import strip_chain_of_thought


class TestStripChainOfThought(unittest.TestCase):
    def test_no_tags_passthrough(self):
        self.assertEqual(strip_chain_of_thought("plain answer"), "plain answer")

    def test_empty_input(self):
        self.assertEqual(strip_chain_of_thought(""), "")
        self.assertEqual(strip_chain_of_thought(None), "")

    def test_removes_closed_think_block(self):
        text = "<think>let me reason about this</think>the answer"
        self.assertEqual(strip_chain_of_thought(text), "the answer")

    def test_removes_closed_block_case_insensitive_and_variants(self):
        for tag in ("think", "THINK", "Thinking", "reasoning"):
            text = f"<{tag}>reasoning here</{tag}>answer"
            self.assertEqual(strip_chain_of_thought(text), "answer")

    def test_removes_unclosed_trailing_block(self):
        # Truncation failure mode: generation is cut off by the token cap
        # mid-thought, so the closing tag never arrives.
        text = "answer so far<think>and then I was reasoning forever with no end"
        self.assertEqual(strip_chain_of_thought(text), "answer so far")

    def test_entirely_reasoning_yields_empty(self):
        text = "<think>nothing but reasoning, cut off"
        self.assertEqual(strip_chain_of_thought(text), "")

    def test_multiple_blocks_removed(self):
        text = "<think>a</think>keep1<think>b</think>keep2"
        self.assertEqual(strip_chain_of_thought(text), "keep1keep2")

    def test_spans_newlines(self):
        text = "<think>line one\nline two\nline three</think>final answer"
        self.assertEqual(strip_chain_of_thought(text), "final answer")


if __name__ == "__main__":
    unittest.main()
