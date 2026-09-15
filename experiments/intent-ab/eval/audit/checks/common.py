"""Shared helpers for the drift (D1-D5) and standing-constraint (SC1-SC12)
checkers: running a target repo's `bract` CLI as a subprocess, and walking
its `bract/` package source for the static checks.

Every checker in checks/drift.py and checks/standing.py takes a single
`repo: Path` (a directory containing an importable `bract/` package, laid
out like the seed repo) and returns a Verdict.
"""
from __future__ import annotations

import ast
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

DEFAULT_TIMEOUT_SECONDS = 10
_PROBE_DIR = Path(tempfile.mkdtemp(prefix="bract_audit_probes_"))


def write_probe(source: str, name: str = "probe.bract") -> Path:
    """Writes a small Bract source probe to a scratch temp directory
    (outside the target repo, so checks never leave files behind in a
    checkout they're auditing) and returns its path.
    """
    fd, raw_path = tempfile.mkstemp(prefix=f"{name}_", suffix=".bract", dir=_PROBE_DIR)
    path = Path(raw_path)
    with open(fd, "w") as f:
        f.write(source)
    return path


@dataclass
class Verdict:
    id: str
    method: str  # "static" | "behavior"
    passed: bool
    detail: str
    evidence: dict = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "method": self.method,
            "passed": self.passed,
            "detail": self.detail,
            "evidence": self.evidence,
        }


@dataclass
class RunResult:
    returncode: int | None
    stdout: str
    stderr: str
    timed_out: bool


def run_bract(
    repo: Path,
    args: list,
    stdin: str | None = None,
    timeout: float = DEFAULT_TIMEOUT_SECONDS,
) -> RunResult:
    """Runs `python3 -m bract <args>` with cwd=repo, the same invocation
    convention as ../runner.py uses against an arm's final `main`.
    """
    try:
        proc = subprocess.run(
            [sys.executable, "-m", "bract", *args],
            cwd=str(repo),
            input=stdin,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return RunResult(proc.returncode, proc.stdout, proc.stderr, timed_out=False)
    except subprocess.TimeoutExpired as e:
        return RunResult(
            None,
            e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or ""),
            e.stderr.decode() if isinstance(e.stderr, bytes) else (e.stderr or ""),
            timed_out=True,
        )


def bract_package_dir(repo: Path) -> Path:
    return repo / "bract"


def iter_py_files(repo: Path):
    """Every .py file under repo/bract/, excluding __pycache__."""
    pkg = bract_package_dir(repo)
    for path in sorted(pkg.rglob("*.py")):
        if "__pycache__" in path.parts:
            continue
        yield path


def iter_py_files_outside_repo_root(repo: Path):
    """Every .py file directly under repo (not inside bract/), for checks
    that care about a *second* top-level entry point living outside the
    package.
    """
    for path in sorted(repo.glob("*.py")):
        yield path


def parse_module(path: Path) -> ast.Module | None:
    try:
        return ast.parse(path.read_text(), filename=str(path))
    except (SyntaxError, OSError):
        return None


def module_level_assign_targets(tree: ast.Module):
    """Yields (name, value_node) for every top-level (module-scope)
    `NAME = value` assignment -- i.e. not nested inside a function or
    class body.
    """
    for node in tree.body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name):
                    yield target.id, node.value
        elif isinstance(node, ast.AnnAssign) and node.value is not None:
            if isinstance(node.target, ast.Name):
                yield node.target.id, node.value


def calls_named(tree: ast.AST, names: set):
    """Yields Call nodes whose callee is a bare Name in `names` or an
    Attribute whose `.attr` is in `names` (e.g. `eval(...)` or
    `builtins.eval(...)`).
    """
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        if isinstance(func, ast.Name) and func.id in names:
            yield node
        elif isinstance(func, ast.Attribute) and func.attr in names:
            yield node
