"""Scaffold smoke test. Later tasks add real lexer/parser/evaluator/CLI
tests alongside their modules -- this only checks the package imports and
the CLI dispatch skeleton behaves per DESIGN.md section 9 before any
language feature exists.
"""
import unittest

import bract
from bract.__main__ import main


class TestScaffold(unittest.TestCase):
    def test_package_importable(self):
        self.assertTrue(hasattr(bract, "__version__"))

    def test_cli_usage_on_bad_args(self):
        self.assertEqual(main(["bogus"]), 2)


if __name__ == "__main__":
    unittest.main()
