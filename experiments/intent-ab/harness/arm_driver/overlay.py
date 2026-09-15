"""Apply file overlays on top of a cloned project tree.

See ../arms/README.md for the composition order (cloned seed, then the
bonsai wrapper source, then common/, then the arm-specific directory).
Each overlay directory's tree is copied onto the target, later overlays
winning file-by-file; nothing is ever deleted from the target by an
overlay.
"""
import shutil
from pathlib import Path

_IGNORE = shutil.ignore_patterns("__pycache__", "*.pyc")


def apply_overlay(target_dir: Path, overlay_dirs) -> None:
    """Copy each overlay directory's *contents* onto `target_dir`, in
    order — later overlays win on a file-path collision. Each overlay dir
    mirrors the target's own layout (e.g. it contains `.cloche/...`
    directly), unlike `copy_into`."""
    target_dir = Path(target_dir)
    for overlay_dir in overlay_dirs:
        overlay_dir = Path(overlay_dir)
        if not overlay_dir.is_dir():
            raise FileNotFoundError(f"overlay directory does not exist: {overlay_dir}")
        shutil.copytree(overlay_dir, target_dir, dirs_exist_ok=True, ignore=_IGNORE)


def copy_into(target_dir: Path, name: str, source_dir: Path) -> None:
    """Copy `source_dir`'s contents into `target_dir/name` (creating it).
    Used to place the bonsai wrapper's live source (harness/agent_command/,
    harness/bin/) into a cloned arm tree, rather than duplicating it as a
    static overlay file that could drift from E4's actual wrapper code."""
    shutil.copytree(source_dir, Path(target_dir) / name, dirs_exist_ok=True, ignore=_IGNORE)
