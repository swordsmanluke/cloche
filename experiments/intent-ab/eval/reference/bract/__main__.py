"""Bract CLI entry point. See DESIGN.md section 9 for the three subcommands.

This is the reference implementation built for experiments/intent-ab (E3):
a correct-by-spec implementation used to validate the hidden acceptance
corpus itself, kept out of both experiment arms.
"""
import sys

from .errors import BractError, LexError, ParseError
from .evaluator import Interpreter, is_nil
from .lexer import lex
from .parser import parse


def _render_value(value) -> str:
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "nil"
    if isinstance(value, str):
        return value
    return str(value)


def repl() -> int:
    interp = Interpreter()
    while True:
        try:
            line = input("bract> ")
        except EOFError:
            print()
            return 0
        if not line.strip():
            continue
        try:
            tokens = lex(line)
            statements = parse(tokens)
        except BractError as e:
            print(e.format(), file=sys.stderr)
            continue
        try:
            for stmt in statements:
                from . import ast_nodes as ast

                if isinstance(stmt, ast.ExprStmt):
                    value = interp.evaluate(stmt.expr, interp.globals)
                    if not is_nil(value):
                        print(_render_value(value))
                else:
                    interp.exec_stmt(stmt, interp.globals)
        except BractError as e:
            print(e.format(), file=sys.stderr)


def run_file(path: str) -> int:
    try:
        with open(path, "r") as f:
            source = f.read()
    except OSError as e:
        print(f"bract: cannot read '{path}': {e}", file=sys.stderr)
        return 2

    try:
        tokens = lex(source)
        statements = parse(tokens)
    except (LexError, ParseError) as e:
        print(e.format(), file=sys.stderr)
        return 2

    try:
        Interpreter().run(statements)
    except BractError as e:
        print(e.format(), file=sys.stderr)
        return 1

    return 0


def fmt_file(path: str) -> int:
    try:
        with open(path, "r") as f:
            source = f.read()
    except OSError as e:
        print(f"bract: cannot read '{path}': {e}", file=sys.stderr)
        return 2

    try:
        tokens = lex(source)
        statements = parse(tokens)
    except (LexError, ParseError) as e:
        print(e.format(), file=sys.stderr)
        return 2

    from .formatter import format_program

    sys.stdout.write(format_program(statements))
    return 0


def main(argv: list) -> int:
    if not argv:
        return repl()
    if argv[0] == "run" and len(argv) == 2:
        return run_file(argv[1])
    if argv[0] == "fmt" and len(argv) == 2:
        return fmt_file(argv[1])
    print("usage: bract | bract run FILE | bract fmt FILE", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
