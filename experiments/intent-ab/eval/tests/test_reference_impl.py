"""Unit tests for the reference Bract implementation itself (E3), as
opposed to the acceptance-corpus-level tests in test_corpus_acceptance.py.
These exercise the reference bract/ package's Python API directly.
"""
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "reference"))

from bract.errors import BractError  # noqa: E402
from bract.evaluator import Interpreter  # noqa: E402
from bract.lexer import lex  # noqa: E402
from bract.parser import parse  # noqa: E402


def run_source(source: str):
    """Returns (exit_code, error_line_message_or_None)."""
    try:
        tokens = lex(source)
        statements = parse(tokens)
    except BractError as e:
        return 2, e.format()
    try:
        Interpreter().run(statements)
    except BractError as e:
        return 1, e.format()
    return 0, None


class TestLexer(unittest.TestCase):
    def test_bang_is_lex_error(self):
        with self.assertRaises(BractError) as ctx:
            lex("1 != 2")
        self.assertEqual(ctx.exception.format(), "line 1: unexpected character '!'")

    def test_noteq_token(self):
        tokens = lex("1 <> 2")
        self.assertEqual([t.kind for t in tokens], ["INT", "NOTEQ", "INT", "EOF"])

    def test_unterminated_string(self):
        with self.assertRaises(BractError) as ctx:
            lex('"abc')
        self.assertEqual(ctx.exception.format(), "line 1: unterminated string")

    def test_unknown_escape(self):
        with self.assertRaises(BractError) as ctx:
            lex('"a\\qb"')
        self.assertEqual(
            ctx.exception.format(), "line 1: unknown escape sequence '\\q'"
        )


class TestParser(unittest.TestCase):
    def test_chained_comparison_rejected(self):
        exit_code, msg = run_source("let x = 1 < 2 < 3;")
        self.assertEqual(exit_code, 2)
        self.assertEqual(msg, "line 1: comparisons do not chain")

    def test_bare_assignment_rejected(self):
        exit_code, _ = run_source("x = 5;")
        self.assertEqual(exit_code, 2)


class TestEvaluator(unittest.TestCase):
    def test_let_redeclare_same_scope_errors(self):
        exit_code, msg = run_source("let x = 1;\nlet x = 2;")
        self.assertEqual(exit_code, 1)
        self.assertEqual(msg, "line 2: 'x' is already declared")

    def test_let_shadow_inner_scope_ok(self):
        exit_code, _ = run_source(
            "let x = 1;\nif (true) {\n  let x = 2;\n}\n"
        )
        self.assertEqual(exit_code, 0)

    def test_condition_must_be_boolean(self):
        exit_code, msg = run_source("if (0) {\n}")
        self.assertEqual(exit_code, 1)
        self.assertEqual(msg, "line 1: condition must be a boolean")

    def test_and_or_evaluate_both_operands(self):
        source = (
            "let counter = 0;\n"
            "fn bump() {\n"
            "  set counter = counter + 1;\n"
            "  return true;\n"
            "}\n"
            "let ok = false and bump();\n"
        )
        exit_code, _ = run_source(source)
        self.assertEqual(exit_code, 0)
        # Re-run through the interpreter directly to inspect the counter.
        tokens = lex(source)
        statements = parse(tokens)
        interp = Interpreter()
        interp.run(statements)
        self.assertEqual(interp.globals.get("counter", 0), 1)

    def test_negative_division_truncates_toward_zero(self):
        interp = Interpreter()
        statements = parse(lex("let q = -7 / 2;\nlet r = -7 % 2;"))
        interp.run(statements)
        self.assertEqual(interp.globals.get("q", 0), -3)
        self.assertEqual(interp.globals.get("r", 0), -1)

    def test_string_builtins_are_one_indexed(self):
        interp = Interpreter()
        statements = parse(lex('let c = at("hello", 1);\nlet s = sub("hello", 1, 3);'))
        interp.run(statements)
        self.assertEqual(interp.globals.get("c", 0), "h")
        self.assertEqual(interp.globals.get("s", 0), "hel")

    def test_closure_counter(self):
        source = (
            "fn make_counter() {\n"
            "  let n = 0;\n"
            "  fn next() {\n"
            "    set n = n + 1;\n"
            "    return n;\n"
            "  }\n"
            "  return next;\n"
            "}\n"
            "let counter = make_counter();\n"
            "let a = counter();\n"
            "let b = counter();\n"
        )
        interp = Interpreter()
        interp.run(parse(lex(source)))
        self.assertEqual(interp.globals.get("a", 0), 1)
        self.assertEqual(interp.globals.get("b", 0), 2)


if __name__ == "__main__":
    unittest.main()
