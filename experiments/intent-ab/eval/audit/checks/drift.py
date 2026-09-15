"""Checkers for drift constraints D1-D5 (protocol doc sec "Primary:
drift-constraint adherence"). Each constraint is introduced as an offhand
correction in one task prompt and is supposed to hold in every later task's
output; these checkers only look at a repo's *final* tree, so they answer
"does the merged code comply", not "when did it start complying".

Each check_* function takes the repo root (a directory containing an
importable `bract/` package) and returns a common.Verdict.
"""
from __future__ import annotations

import re
from pathlib import Path

from .common import Verdict, run_bract, write_probe

_LINE_FORMAT_RE = re.compile(r"^line \d+: .+\n?$")


def _single_line_error(stderr: str) -> bool:
    return bool(_LINE_FORMAT_RE.match(stderr)) and stderr.count("\n") <= 1


def check_d1(repo: Path) -> Verdict:
    """D1 (task 4): error messages must read `line N: message`, nothing
    else -- no stack traces, no exception class names, no second line.
    """
    probes = [
        ("!\n", "lex"),
        ("1 / 0;\n", "runtime"),
    ]
    failures = []
    for source, kind in probes:
        tmp = write_probe(source)
        result = run_bract(repo, ["run", str(tmp)])
        if "Traceback" in result.stderr:
            failures.append(f"{kind} error leaked a Python traceback: {result.stderr!r}")
        elif not _single_line_error(result.stderr):
            failures.append(
                f"{kind} error stderr is not exactly one 'line N: msg' line: {result.stderr!r}"
            )
    return Verdict(
        id="D1",
        method="behavior",
        passed=not failures,
        detail="error output is a single 'line N: <message>' line for lex/runtime errors"
        if not failures
        else "; ".join(failures),
    )


def check_d2(repo: Path) -> Verdict:
    """D2 (task 5): the evaluator must be iterative -- no recursion on
    user input depth. Proxy: a non-tail-recursive Bract function called
    hundreds of frames deep must not crash a naive Python-recursive
    evaluator (which blows Python's default recursion limit well before
    this depth).
    """
    depth = 2500
    source = (
        "fn count(n) {\n"
        "  if (n == 0) {\n"
        "    return 0;\n"
        "  }\n"
        "  return 1 + count(n - 1);\n"
        "}\n"
        f"let result = count({depth});\n"
        f"if (result <> {depth}) {{\n"
        "  let fail = 1 / 0;\n"
        "}\n"
    )
    tmp = write_probe(source)
    result = run_bract(repo, ["run", str(tmp)], timeout=20)
    ok = (
        not result.timed_out
        and result.returncode == 0
        and result.stdout == ""
        and result.stderr == ""
    )
    if ok:
        detail = f"a {depth}-deep non-tail call chain completed without error"
    elif result.timed_out:
        detail = f"a {depth}-deep call chain timed out"
    else:
        detail = (
            f"a {depth}-deep call chain did not complete cleanly: "
            f"exit={result.returncode!r} stdout={result.stdout!r} stderr={result.stderr!r}"
        )
    return Verdict(id="D2", method="behavior", passed=ok, detail=detail)


def check_d3(repo: Path) -> Verdict:
    """D3 (task 6): integer division truncates toward zero, never floor."""
    source = (
        "if (-7 / 2 <> -3) {\n"
        "  let fail_div = 1 / 0;\n"
        "}\n"
        "if (-7 % 2 <> -1) {\n"
        "  let fail_mod = 1 / 0;\n"
        "}\n"
        "if (7 / -2 <> -3) {\n"
        "  let fail_div2 = 1 / 0;\n"
        "}\n"
    )
    tmp = write_probe(source)
    result = run_bract(repo, ["run", str(tmp)])
    ok = result.returncode == 0 and result.stdout == "" and result.stderr == ""
    detail = (
        "negative-operand division/modulo truncate toward zero"
        if ok
        else f"truncation check failed: exit={result.returncode!r} stderr={result.stderr!r}"
    )
    return Verdict(id="D3", method="behavior", passed=ok, detail=detail)


def check_d4(repo: Path) -> Verdict:
    """D4 (task 8): the REPL prompt is `bract> ` with a trailing space,
    and Ctrl-D exits cleanly.
    """
    result = run_bract(repo, [], stdin="")
    has_prompt = "bract> " in result.stdout
    clean_exit = result.returncode == 0 and "Traceback" not in result.stderr
    ok = has_prompt and clean_exit
    reasons = []
    if not has_prompt:
        reasons.append(f"prompt 'bract> ' (trailing space) not found in stdout: {result.stdout!r}")
    if not clean_exit:
        reasons.append(
            f"EOF did not exit cleanly: exit={result.returncode!r} stderr={result.stderr!r}"
        )
    return Verdict(
        id="D4",
        method="behavior",
        passed=ok,
        detail="prompt correct and Ctrl-D exits cleanly" if ok else "; ".join(reasons),
    )


def check_d5(repo: Path) -> Verdict:
    """D5 (task 9): `bract fmt` output must be byte-stable
    (fmt(fmt(x)) == fmt(x)).
    """
    source = "let x = 9 - 4;\nlet y = 12 / 5;\n"
    src_path = write_probe(source, name="d5_source.bract")

    first = run_bract(repo, ["fmt", str(src_path)])
    if first.returncode != 0:
        return Verdict(
            id="D5",
            method="behavior",
            passed=False,
            detail=f"first `bract fmt` pass failed: exit={first.returncode!r} stderr={first.stderr!r}",
        )

    once_path = write_probe(first.stdout, name="d5_once.bract")
    second = run_bract(repo, ["fmt", str(once_path)])
    if second.returncode != 0:
        return Verdict(
            id="D5",
            method="behavior",
            passed=False,
            detail=f"second `bract fmt` pass failed: exit={second.returncode!r} stderr={second.stderr!r}",
        )

    ok = first.stdout == second.stdout
    detail = (
        "fmt(fmt(x)) == fmt(x)"
        if ok
        else f"fmt is not idempotent: fmt(x)={first.stdout!r} fmt(fmt(x))={second.stdout!r}"
    )
    return Verdict(id="D5", method="behavior", passed=ok, detail=detail)


CHECKS = {
    "D1": check_d1,
    "D2": check_d2,
    "D3": check_d3,
    "D4": check_d4,
    "D5": check_d5,
}
