#!/usr/bin/env python3
"""Builds the D1-D5 / SC1-SC12 violation fixture trees under
fixtures/violations/<ID>/ from fixtures/compliant/, by copying the
compliant tree and applying one small, hand-written patch per constraint.

Each patch is built to break exactly the one constraint it's named for,
verified by tests/test_checks.py (which also asserts every *other*
constraint still passes on that same tree). Re-run this script whenever a
patch changes; it always regenerates every violation tree from scratch.

Usage: python3 build_violations.py
"""
import shutil
from pathlib import Path

HERE = Path(__file__).resolve().parent
COMPLIANT = HERE / "compliant"
VIOLATIONS_DIR = HERE / "violations"


def _read(rel_path: str) -> str:
    return (COMPLIANT / rel_path).read_text()


def _patch_replace(dest: Path, rel_path: str, old: str, new: str) -> None:
    path = dest / rel_path
    text = path.read_text()
    if old not in text:
        raise ValueError(f"{rel_path}: patch anchor not found for a violation in {dest.name}")
    patched = text.replace(old, new, 1)
    if patched == text:
        raise ValueError(f"{rel_path}: patch was a no-op for a violation in {dest.name}")
    path.write_text(patched)


def _write_new_file(dest: Path, rel_path: str, content: str) -> None:
    path = dest / rel_path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


def build_tree(check_id: str, build_fn) -> None:
    dest = VIOLATIONS_DIR / check_id
    if dest.exists():
        shutil.rmtree(dest)
    shutil.copytree(COMPLIANT, dest)
    build_fn(dest)


def d1_bad_error_format(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/errors.py",
        'return f"line {self.line}: {self.message}"',
        'return f"line {self.line}: {self.message}\\n(no further details)"',
    )


def d2_recursive_evaluator(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/__main__.py",
        """    threading.stack_size(_WORKER_STACK_SIZE)
    result_box = {}
    worker = threading.Thread(target=_run_deep, args=(statements, result_box))
    worker.start()
    worker.join()

    if "error" in result_box:
        print(result_box["error"].format(), file=sys.stderr)
        return 1
    return 0""",
        """    try:
        Interpreter().run(statements)
    except BractError as e:
        print(e.format(), file=sys.stderr)
        return 1
    return 0""",
    )


def d3_floor_division(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/evaluator.py",
        """def _trunc_divmod(a: int, b: int, line: int):
    if b == 0:
        raise RuntimeErrorBract(line, "division by zero")
    q = abs(a) // abs(b)
    if (a < 0) != (b < 0):
        q = -q
    r = a - q * b
    return q, r""",
        """def _trunc_divmod(a: int, b: int, line: int):
    if b == 0:
        raise RuntimeErrorBract(line, "division by zero")
    return a // b, a % b""",
    )


def d4_bad_repl(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/__main__.py",
        """    while True:
        try:
            line = input("bract> ")
        except EOFError:
            print()
            return 0
        if not line.strip():""",
        """    while True:
        line = input("bract>")
        if not line.strip():""",
    )


def d5_fmt_not_stable(dest: Path) -> None:
    _swap_binary_operands(dest)


def sc9_fmt_reorders(dest: Path) -> None:
    _swap_binary_operands(dest)


def _swap_binary_operands(dest: Path) -> None:
    # Swapping the printed operand order for non-commutative operators
    # both breaks fmt's idempotence (D5) and changes program behavior
    # after a format round-trip (SC9) -- see fixtures/README.md.
    _patch_replace(
        dest,
        "bract/formatter.py",
        """    if isinstance(node, ast.Binary):
        return f"{_fmt_expr(node.left)} {node.op} {_fmt_expr(node.right)}\"""",
        """    if isinstance(node, ast.Binary):
        if node.op in ("-", "/"):
            return f"{_fmt_expr(node.right)} {node.op} {_fmt_expr(node.left)}"
        return f"{_fmt_expr(node.left)} {node.op} {_fmt_expr(node.right)}\"""",
    )


def sc1_third_party_import(dest: Path) -> None:
    _write_new_file(
        dest,
        "bract/scratch.py",
        '"""Planted for the SC1 fixture; never imported elsewhere."""\n'
        "import numpy  # third-party, not stdlib\n",
    )


def sc2_nested_subpackage(dest: Path) -> None:
    _write_new_file(
        dest,
        "bract/utils/__init__.py",
        '"""Planted for the SC2 fixture: a nested subpackage under bract/."""\n',
    )


def sc3_ad_hoc_error_format(dest: Path) -> None:
    _write_new_file(
        dest,
        "bract/_debug.py",
        '"""Planted for the SC3 fixture: a second place that formats a\n'
        "'line N: msg' string, outside the shared errors.py path. Never\n"
        'called elsewhere.\n"""\n\n\n'
        "def _debug_format(line, message):\n"
        '    return f"line {line}: {message}"\n',
    )


def sc4_print_outside_cli(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/evaluator.py",
        "class ReturnSignal(Exception):",
        "def _debug_dump(value):  # planted for the SC4 fixture; never called\n"
        "    print(value)\n\n\n"
        "class ReturnSignal(Exception):",
    )


def sc5_lowercase_token_names(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/lexer.py",
        "ESCAPES = {",
        'TOKEN_ALIASES = {  # planted for the SC5 fixture; unused, deliberately lowercase\n'
        '    "let": "let_tok",\n'
        '    "plus": "plus_tok",\n'
        "}\n\n"
        "ESCAPES = {",
    )


def sc6_bad_exit_code(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/__main__.py",
        """    try:
        tokens = lex(source)
        statements = parse(tokens)
    except (LexError, ParseError) as e:
        print(e.format(), file=sys.stderr)
        return 2

    threading.stack_size""",
        """    try:
        tokens = lex(source)
        statements = parse(tokens)
    except (LexError, ParseError) as e:
        print(e.format(), file=sys.stderr)
        return 42

    threading.stack_size""",
    )


def sc7_uses_eval(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/evaluator.py",
        "class ReturnSignal(Exception):",
        "def _debug_eval(expr_text):  # planted for the SC7 fixture; never called\n"
        "    return eval(expr_text)\n\n\n"
        "class ReturnSignal(Exception):",
    )


def sc8_module_global_state(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/evaluator.py",
        "class ReturnSignal(Exception):",
        "_CALL_LOG = []  # planted for the SC8 fixture: module-level mutable state\n\n\n"
        "def _record_call(name):  # never called; the module-level mutation is what's flagged\n"
        "    global _CALL_LOG\n"
        "    _CALL_LOG.append(name)\n\n\n"
        "class ReturnSignal(Exception):",
    )


def sc10_second_entry_point(dest: Path) -> None:
    _write_new_file(
        dest,
        "run_bract.py",
        '"""Planted for the SC10 fixture: a second top-level entry point\n'
        'that duplicates bract/__main__.py\'s dispatch instead of routing\n'
        'through it."""\n'
        "import sys\n\n"
        "from bract.__main__ import main\n\n"
        'if __name__ == "__main__":\n'
        "    sys.exit(main(sys.argv[1:]))\n",
    )


def sc11_zero_indexed_lines(dest: Path) -> None:
    _patch_replace(dest, "bract/lexer.py", "    line = 1\n", "    line = 0\n")


def sc12_todo_stub(dest: Path) -> None:
    _patch_replace(
        dest,
        "bract/evaluator.py",
        "class ReturnSignal(Exception):",
        "# TODO: revisit this once the fixture is done\n\n\n"
        "class ReturnSignal(Exception):",
    )


BUILDERS = {
    "D1": d1_bad_error_format,
    "D2": d2_recursive_evaluator,
    "D3": d3_floor_division,
    "D4": d4_bad_repl,
    "D5": d5_fmt_not_stable,
    "SC1": sc1_third_party_import,
    "SC2": sc2_nested_subpackage,
    "SC3": sc3_ad_hoc_error_format,
    "SC4": sc4_print_outside_cli,
    "SC5": sc5_lowercase_token_names,
    "SC6": sc6_bad_exit_code,
    "SC7": sc7_uses_eval,
    "SC8": sc8_module_global_state,
    "SC9": sc9_fmt_reorders,
    "SC10": sc10_second_entry_point,
    "SC11": sc11_zero_indexed_lines,
    "SC12": sc12_todo_stub,
}


def main() -> None:
    VIOLATIONS_DIR.mkdir(exist_ok=True)
    for check_id, builder in BUILDERS.items():
        build_tree(check_id, builder)
        print(f"built fixtures/violations/{check_id}/")


if __name__ == "__main__":
    main()
