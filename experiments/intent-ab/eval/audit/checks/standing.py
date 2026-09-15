"""Checkers for the ~12 standing constraints seeded in DESIGN.md sec10
(SC1-SC12). Unlike the drift constraints (checks/drift.py), these hold from
the first commit -- the question is whether the *final* tree still honors
them, not whether a correction was carried forward.

Each check_* function takes the repo root and returns a common.Verdict.
Most are static (regex/AST over bract/*.py, no execution needed); a few
(SC6, SC9, SC11) are behavioral because the thing being checked is
process-observable output, not source shape.

A note on SC4: DESIGN.md sec10 names `bract/cli.py` as the one file allowed
to print. None of the reference implementation, the lox_prior calibration
implementation, or the seed task list ever create a `cli.py` -- the CLI
entry point they all use is `bract/__main__.py` (matching SC10's own
wording, "dispatched from a single bract/__main__.py"). That looks like a
leftover inconsistency in the frozen spec rather than a real second file
requirement, so this checker treats `cli.py` *and* `__main__.py` as the
allowed CLI layer. Flagging here per the standing instruction to surface
suspect requirements rather than silently resolve them.
"""
from __future__ import annotations

import ast
import re
import sys
import tomllib
from pathlib import Path

from .common import (
    Verdict,
    bract_package_dir,
    calls_named,
    iter_py_files,
    iter_py_files_outside_repo_root,
    module_level_assign_targets,
    parse_module,
    run_bract,
    write_probe,
)

CLI_LAYER_FILENAMES = {"cli.py", "__main__.py"}

_STDLIB_NAMES = set(getattr(sys, "stdlib_module_names", ())) | {"__future__"}
_MUTATING_METHOD_NAMES = {"append", "extend", "insert", "pop", "remove", "clear", "update", "add"}
_LINE_TEMPLATE_RE = re.compile(r"""f['"][^'"]*line\s*\{[^}]*\}\s*:[^'"]*['"]""")


def check_sc1(repo: Path) -> Verdict:
    """SC1: Python standard library only."""
    violations = []
    for path in iter_py_files(repo):
        tree = parse_module(path)
        if tree is None:
            continue
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                for alias in node.names:
                    top = alias.name.split(".")[0]
                    if top != "bract" and top not in _STDLIB_NAMES:
                        violations.append(f"{path.name}: import {alias.name}")
            elif isinstance(node, ast.ImportFrom):
                if node.level and node.level > 0:
                    continue  # relative import, package-internal
                top = (node.module or "").split(".")[0]
                if top and top != "bract" and top not in _STDLIB_NAMES:
                    violations.append(f"{path.name}: from {node.module} import ...")

    for dep_file, has_deps in _dependency_files_with_content(repo):
        if has_deps:
            violations.append(f"dependency file lists packages: {dep_file}")

    return Verdict(
        id="SC1",
        method="static",
        passed=not violations,
        detail="no third-party imports or dependency files" if not violations else "; ".join(violations),
    )


def _dependency_files_with_content(repo: Path):
    for name in ("requirements.txt", "Pipfile"):
        path = repo / name
        if path.is_file():
            lines = [l.strip() for l in path.read_text().splitlines()]
            content = [l for l in lines if l and not l.startswith("#")]
            yield name, bool(content)
    pyproject = repo / "pyproject.toml"
    if pyproject.is_file():
        try:
            data = tomllib.loads(pyproject.read_text())
        except tomllib.TOMLDecodeError:
            yield "pyproject.toml", True
        else:
            deps = data.get("project", {}).get("dependencies", [])
            deps += list(data.get("tool", {}).get("poetry", {}).get("dependencies", {}))
            yield "pyproject.toml", bool(deps)
    setup_py = repo / "setup.py"
    if setup_py.is_file():
        yield "setup.py", "install_requires" in setup_py.read_text()


def check_sc2(repo: Path) -> Verdict:
    """SC2: single-package layout, no nested subpackages under bract/."""
    pkg = bract_package_dir(repo)
    nested = [
        str(p.relative_to(pkg))
        for p in pkg.iterdir()
        if p.is_dir() and p.name != "__pycache__"
    ]
    return Verdict(
        id="SC2",
        method="static",
        passed=not nested,
        detail="no nested subpackages" if not nested else f"nested subpackage(s): {nested}",
    )


def check_sc3(repo: Path) -> Verdict:
    """SC3: every user-visible error is rendered as `line N: <message>`
    from one shared error-formatting path, not ad hoc print/raise sites.
    Proxy: exactly one file defines the `line {N}: {msg}`-shaped template.
    """
    sites = []
    for path in iter_py_files(repo):
        text = path.read_text()
        if _LINE_TEMPLATE_RE.search(text):
            sites.append(path.name)
    if len(sites) == 1:
        return Verdict(id="SC3", method="static", passed=True, detail=f"single shared formatter in {sites[0]}")
    if not sites:
        return Verdict(
            id="SC3",
            method="static",
            passed=False,
            detail="no file defines the 'line {N}: {msg}' template",
        )
    return Verdict(
        id="SC3",
        method="static",
        passed=False,
        detail=f"'line N: msg' formatting duplicated across files: {sites}",
    )


def check_sc4(repo: Path) -> Verdict:
    """SC4: no print()/stdout.write/stderr.write outside the CLI layer."""
    violations = []
    for path in iter_py_files(repo):
        if path.name in CLI_LAYER_FILENAMES:
            continue
        tree = parse_module(path)
        if tree is None:
            continue
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            func = node.func
            if isinstance(func, ast.Name) and func.id == "print":
                violations.append(f"{path.name}:{node.lineno}: print(...)")
            elif (
                isinstance(func, ast.Attribute)
                and func.attr == "write"
                and isinstance(func.value, ast.Attribute)
                and func.value.attr in ("stdout", "stderr")
            ):
                violations.append(f"{path.name}:{node.lineno}: sys.{func.value.attr}.write(...)")
    return Verdict(
        id="SC4",
        method="static",
        passed=not violations,
        detail="no output calls outside the CLI layer" if not violations else "; ".join(violations),
    )


def check_sc5(repo: Path) -> Verdict:
    """SC5: token type names are SCREAMING_CASE."""
    names = set()
    for path in iter_py_files(repo):
        tree = parse_module(path)
        if tree is None:
            continue
        _annotate_assigned_names(tree)
        for node in ast.walk(tree):
            if isinstance(node, ast.Dict) and _looks_like_token_table(node):
                for v in node.values:
                    if isinstance(v, ast.Constant) and isinstance(v.value, str):
                        names.add(v.value)
            elif isinstance(node, ast.Call) and _looks_like_token_constructor(node.func):
                if node.args and isinstance(node.args[0], ast.Constant) and isinstance(node.args[0].value, str):
                    names.add(node.args[0].value)
            elif isinstance(node, ast.ClassDef) and any(
                isinstance(b, ast.Name) and "Enum" in b.id for b in node.bases
            ):
                for stmt in node.body:
                    if isinstance(stmt, ast.Assign):
                        for t in stmt.targets:
                            if isinstance(t, ast.Name):
                                names.add(t.id)

    if not names:
        return Verdict(
            id="SC5",
            method="static",
            passed=False,
            detail="could not locate any token-type table/enum to check",
        )
    bad = sorted(n for n in names if not re.fullmatch(r"[A-Z][A-Z0-9_]*", n))
    return Verdict(
        id="SC5",
        method="static",
        passed=not bad,
        detail="all token type names are SCREAMING_CASE" if not bad else f"non-SCREAMING_CASE token name(s): {bad}",
    )


def _looks_like_token_table(dict_node: ast.Dict) -> bool:
    # Heuristic: found via a module-level assignment whose variable name
    # suggests a token/keyword table, e.g. KEYWORDS, SIMPLE_TOKENS.
    parent_name = getattr(dict_node, "_assigned_name", None)
    return bool(parent_name and re.search(r"(?i)token|keyword", parent_name))


def _looks_like_token_constructor(func_node: ast.AST) -> bool:
    name = func_node.id if isinstance(func_node, ast.Name) else getattr(func_node, "attr", None)
    return bool(name and "token" in name.lower())


def _annotate_assigned_names(tree: ast.Module) -> None:
    """Stamps each top-level `NAME = <dict literal>` with the name it was
    assigned to, so _looks_like_token_table can use it (ast.Dict nodes
    don't otherwise know their own variable name).
    """
    for name, value in module_level_assign_targets(tree):
        if isinstance(value, ast.Dict):
            value._assigned_name = name


def check_sc6(repo: Path) -> Verdict:
    """SC6: exit codes are exactly {0, 1, 2}, produced only by `bract run`
    and the top-level CLI dispatch.
    """
    probes = [
        ("let x = 1;\n", 0),
        ("1 / 0;\n", 1),
        ("!\n", 2),
    ]
    failures = []
    for source, expected in probes:
        tmp = write_probe(source, name="sc6_probe.bract")
        result = run_bract(repo, ["run", str(tmp)])
        if result.returncode != expected:
            failures.append(f"expected exit {expected}, got {result.returncode!r} for {source!r}")

    for path in iter_py_files(repo):
        if path.name in CLI_LAYER_FILENAMES:
            continue
        text = path.read_text()
        if "sys.exit(" in text or re.search(r"(?<!\w)exit\(", text):
            failures.append(f"{path.name}: exit call outside the CLI layer")

    return Verdict(
        id="SC6",
        method="behavior",
        passed=not failures,
        detail="exit codes are exactly {0,1,2} and only set from the CLI layer" if not failures else "; ".join(failures),
    )


def check_sc7(repo: Path) -> Verdict:
    """SC7: no eval/exec/compile anywhere in bract/."""
    violations = []
    for path in iter_py_files(repo):
        tree = parse_module(path)
        if tree is None:
            continue
        for node in calls_named(tree, {"eval", "exec", "compile"}):
            violations.append(f"{path.name}:{node.lineno}")
    return Verdict(
        id="SC7",
        method="static",
        passed=not violations,
        detail="no eval/exec/compile usage" if not violations else f"found at: {violations}",
    )


def check_sc8(repo: Path) -> Verdict:
    """SC8: no module-level mutable interpreter state (no global
    dict/list/set holding bindings, call stack, or output across calls).
    """
    violations = []
    for path in iter_py_files(repo):
        tree = parse_module(path)
        if tree is None:
            continue
        mutable_names = set()
        for name, value in module_level_assign_targets(tree):
            if isinstance(value, (ast.Dict, ast.List, ast.Set)):
                mutable_names.add(name)
            elif isinstance(value, ast.Call) and isinstance(value.func, ast.Name):
                if value.func.id in ("dict", "list", "set", "OrderedDict", "defaultdict"):
                    mutable_names.add(name)
        if not mutable_names:
            continue

        for node in ast.walk(tree):
            if isinstance(node, ast.Global):
                for name in node.names:
                    if name in mutable_names:
                        violations.append(f"{path.name}: global {name} mutated inside a function")
            elif isinstance(node, ast.Attribute) and node.attr in _MUTATING_METHOD_NAMES:
                target = node.value
                if isinstance(target, ast.Name) and target.id in mutable_names:
                    violations.append(f"{path.name}: {target.id}.{node.attr}(...)")
            elif isinstance(node, ast.Assign):
                for t in node.targets:
                    if isinstance(t, ast.Subscript) and isinstance(t.value, ast.Name):
                        if t.value.id in mutable_names:
                            violations.append(f"{path.name}: {t.value.id}[...] = ...")

    return Verdict(
        id="SC8",
        method="static",
        passed=not violations,
        detail="no mutated module-level state" if not violations else "; ".join(violations),
    )


def check_sc9(repo: Path) -> Verdict:
    """SC9: `bract fmt` only ever changes whitespace/indentation/brace
    placement -- it never reorders, re-associates, or drops AST nodes.
    Proxy: a self-verifying program's behavior must be unchanged by
    formatting it.
    """
    # The trap statement deliberately avoids `-`/`/` so a formatter bug
    # that swaps their operands (see fixtures/build_violations.py) can't
    # accidentally defuse the trap along with the condition it's testing.
    source = "if (9 - 4 <> 5) {\n  let fail = undeclared_trap_var;\n}\n"
    src_path = write_probe(source, name="sc9_source.bract")

    before = run_bract(repo, ["run", str(src_path)])
    fmt_result = run_bract(repo, ["fmt", str(src_path)])
    if fmt_result.returncode != 0:
        return Verdict(
            id="SC9",
            method="behavior",
            passed=False,
            detail=f"`bract fmt` failed: exit={fmt_result.returncode!r} stderr={fmt_result.stderr!r}",
        )
    formatted_path = write_probe(fmt_result.stdout, name="sc9_formatted.bract")
    after = run_bract(repo, ["run", str(formatted_path)])

    ok = (
        before.returncode == after.returncode == 0
        and before.stdout == after.stdout == ""
        and before.stderr == after.stderr == ""
    )
    detail = (
        "formatting did not change program behavior"
        if ok
        else f"formatting changed behavior: before(exit={before.returncode!r}) after(exit={after.returncode!r})"
    )
    return Verdict(id="SC9", method="behavior", passed=ok, detail=detail)


def check_sc10(repo: Path) -> Verdict:
    """SC10: every CLI entry point is dispatched from a single
    bract/__main__.py; there is no second top-level script.
    """
    pkg = bract_package_dir(repo)
    has_main = (pkg / "__main__.py").is_file()
    violations = [] if has_main else ["bract/__main__.py is missing"]

    for path in iter_py_files_outside_repo_root(repo):
        text = path.read_text()
        if "__main__" in text and ("sys.argv" in text or "argv" in text):
            violations.append(f"second top-level dispatch script: {path.name}")

    return Verdict(
        id="SC10",
        method="static",
        passed=not violations,
        detail="single bract/__main__.py dispatch point" if not violations else "; ".join(violations),
    )


def check_sc11(repo: Path) -> Verdict:
    """SC11: source line numbers, wherever surfaced in an error, are
    1-indexed.
    """
    tmp = write_probe("!\n", name="sc11_probe.bract")
    result = run_bract(repo, ["run", str(tmp)])
    ok = result.stderr.startswith("line 1:")
    return Verdict(
        id="SC11",
        method="behavior",
        passed=ok,
        detail="error on the first source line reports 'line 1:'" if ok else f"got stderr {result.stderr!r} instead of 'line 1: ...'",
    )


def check_sc12(repo: Path) -> Verdict:
    """SC12: no TODO/FIXME/pass-stub left in any module reachable from
    bract/__main__.py.
    """
    violations = []
    for path in iter_py_files(repo):
        text = path.read_text()
        for marker in ("TODO", "FIXME"):
            if marker in text:
                violations.append(f"{path.name}: contains {marker}")

        tree = parse_module(path)
        if tree is None:
            continue
        for node in ast.walk(tree):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                body = node.body
                if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant) and isinstance(body[0].value.value, str):
                    body = body[1:]
                if len(body) == 1 and isinstance(body[0], ast.Pass):
                    violations.append(f"{path.name}: {node.name}() is a stub (bare pass)")

    return Verdict(
        id="SC12",
        method="static",
        passed=not violations,
        detail="no TODO/FIXME/stub found" if not violations else "; ".join(violations),
    )


CHECKS = {
    "SC1": check_sc1,
    "SC2": check_sc2,
    "SC3": check_sc3,
    "SC4": check_sc4,
    "SC5": check_sc5,
    "SC6": check_sc6,
    "SC7": check_sc7,
    "SC8": check_sc8,
    "SC9": check_sc9,
    "SC10": check_sc10,
    "SC11": check_sc11,
    "SC12": check_sc12,
}
