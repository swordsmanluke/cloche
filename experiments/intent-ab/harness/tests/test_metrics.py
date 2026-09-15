import unittest

from arm_driver.metrics import (
    estimate_injected_block_size,
    parse_tokens_line,
    summarize_activity,
)


def _entries():
    return [
        {"kind": "attempt_started", "task_id": "t1", "attempt_id": "a1", "workflow": "develop"},
        {"kind": "step_started", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "implement"},
        {"kind": "step_completed", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "implement", "result": "success"},
        {"kind": "step_started", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "test"},
        {"kind": "step_completed", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "test", "result": "fail"},
        {"kind": "step_started", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "fix-tests"},
        {"kind": "step_completed", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "fix-tests", "result": "success"},
        {"kind": "step_started", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "test"},
        {"kind": "step_completed", "task_id": "t1", "attempt_id": "a1", "workflow": "develop", "step": "test", "result": "success"},
        {"kind": "step_started", "task_id": "t1", "attempt_id": "a1", "workflow": "main", "step": "merge"},
        {"kind": "step_completed", "task_id": "t1", "attempt_id": "a1", "workflow": "main", "step": "merge", "result": "success"},
        {"kind": "attempt_ended", "task_id": "t1", "attempt_id": "a1", "state": "succeeded"},

        {"kind": "attempt_started", "task_id": "t2", "attempt_id": "a2", "workflow": "develop"},
        {"kind": "attempt_ended", "task_id": "t2", "attempt_id": "a2", "state": "failed"},
        # A resumed second attempt for t2 that then succeeds — outcome
        # should follow the *latest* attempt_ended, not the first.
        {"kind": "attempt_started", "task_id": "t2", "attempt_id": "a2b", "workflow": "develop"},
        {"kind": "attempt_ended", "task_id": "t2", "attempt_id": "a2b", "state": "succeeded"},
    ]


class TestSummarizeActivity(unittest.TestCase):
    def test_counts(self):
        summary = summarize_activity(_entries())
        self.assertEqual(summary["tasks_attempted"], 2)
        self.assertEqual(summary["total_attempts"], 3)  # t1 x1, t2 x2
        self.assertEqual(summary["attempts_per_task"], {"t1": 1, "t2": 2})
        self.assertEqual(summary["fix_loop_iterations"], 1)
        self.assertEqual(summary["merges_succeeded"], 1)
        self.assertEqual(summary["merges_failed"], 0)

    def test_task_outcome_follows_latest_attempt(self):
        summary = summarize_activity(_entries())
        self.assertEqual(summary["tasks_succeeded"], 2)  # t1 (first try), t2 (second try)
        self.assertEqual(summary["tasks_failed"], 0)

    def test_empty_input(self):
        summary = summarize_activity([])
        self.assertEqual(summary["tasks_attempted"], 0)
        self.assertEqual(summary["total_attempts"], 0)
        self.assertEqual(summary["fix_loop_iterations"], 0)


class TestParseTokensLine(unittest.TestCase):
    def test_parses_comma_formatted_total(self):
        text = "Task: cloche-abcd\nTokens:  8,714 (claude: 6,624 / codex: 2,090)\n"
        self.assertEqual(parse_tokens_line(text), 8714)

    def test_parses_total_without_breakdown(self):
        self.assertEqual(parse_tokens_line("Tokens:  1,234\n"), 1234)

    def test_none_when_line_absent(self):
        self.assertIsNone(parse_tokens_line("Task: cloche-abcd\nState: pending\n"))

    def test_none_when_text_is_none(self):
        self.assertIsNone(parse_tokens_line(None))

    def test_none_when_text_is_empty(self):
        self.assertIsNone(parse_tokens_line(""))


class TestEstimateInjectedBlockSize(unittest.TestCase):
    def test_none_passthrough(self):
        self.assertIsNone(estimate_injected_block_size(None))

    def test_counts_chars_and_words(self):
        result = estimate_injected_block_size("a b c")
        self.assertEqual(result, {"chars": 5, "words_est": 3})


if __name__ == "__main__":
    unittest.main()
