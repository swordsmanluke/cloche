"""Contamination asserts (E5), checked at every poll tick during a run, not
just at setup — a scan or an errant file copy could in principle happen
mid-run.

- Arm A must never contain a `.cloche/intent/` directory (protocol doc,
  "Confounds & controls").
- Neither arm may ever contain the hidden eval corpus (experiments/intent-ab/eval/,
  E3) — the agents must never see it.
"""
import json
import os
from pathlib import Path

SKIP_DIRS = {".git", ".gitworktrees", "__pycache__"}


class ContaminationError(RuntimeError):
    pass


def _walk_files(tree: Path):
    for root, dirs, files in os.walk(tree):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for name in files:
            yield Path(root) / name


def assert_no_intent_dir(tree: Path) -> None:
    """Arm A invariant: no `.cloche/intent/` anywhere under `tree`."""
    tree = Path(tree)
    intent_dir = tree / ".cloche" / "intent"
    if intent_dir.exists():
        raise ContaminationError(f"arm A contamination: {intent_dir} exists")
    # Belt-and-suspenders: catch an intent/ directory under any nested
    # .cloche/ (e.g. inside a stray worktree copy), not just the top level.
    for root, dirs, _files in os.walk(tree):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        if os.path.basename(root) == ".cloche" and "intent" in dirs:
            raise ContaminationError(f"arm A contamination: {os.path.join(root, 'intent')} exists")


def corpus_filenames(eval_corpus_dir: Path) -> set:
    eval_corpus_dir = Path(eval_corpus_dir)
    with open(eval_corpus_dir / "manifest.json") as f:
        manifest = json.load(f)
    return {case["file"] for case in manifest}


def assert_no_eval_corpus(tree: Path, eval_corpus_dir: Path) -> None:
    """Neither arm's tree may contain the hidden acceptance corpus (E3)."""
    tree = Path(tree)
    if (tree / "eval").is_dir():
        raise ContaminationError(f"hidden corpus contamination: {tree / 'eval'} exists")
    names = corpus_filenames(eval_corpus_dir)
    for path in _walk_files(tree):
        if path.name in names:
            raise ContaminationError(
                f"hidden corpus contamination: {path} matches an eval corpus filename"
            )
