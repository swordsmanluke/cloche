#!/usr/bin/env python3
"""Blinded-judging prep (E6): copies both arms' final trees, strips
`.cloche/` and git history, randomizes which copy is labeled which, and
emits a judge bundle -- see protocol doc sec "Qualitative: blinded
pairwise judging".

The judge only ever sees the bundle directory: two anonymously-named
subdirectories (`tree_1`, `tree_2`) with no `.cloche/`, no `.git/`, and no
naming hint about which source arm produced which. The real mapping is
written to a *separate* key file, outside the bundle directory, so the
person running the experiment can keep it while handing the bundle itself
to the judge.

Usage:
    python3 judge_bundle.py --arm-a PATH --arm-b PATH --out DIR [--seed N]

Prints a JSON summary (bundle dir, key file path, labels) to stdout.
"""
from __future__ import annotations

import argparse
import json
import random
import shutil
import sys
import time
from pathlib import Path

STRIP_NAMES = {".cloche", ".git"}
TREE_LABELS = ("tree_1", "tree_2")


def _copy_stripped(src: Path, dest: Path) -> list:
    """Copies src -> dest, omitting any directory named in STRIP_NAMES.
    Returns the list of top-level paths that were stripped (relative to
    src), for the bundle manifest.
    """
    stripped = []

    def _ignore(dir_path, names):
        to_skip = [n for n in names if n in STRIP_NAMES]
        if to_skip:
            rel = Path(dir_path).relative_to(src)
            for n in to_skip:
                stripped.append(str(rel / n) if str(rel) != "." else n)
        return to_skip

    shutil.copytree(src, dest, ignore=_ignore)
    return stripped


def build_bundle(arm_a: Path, arm_b: Path, out_dir: Path, seed: int | None = None) -> dict:
    if out_dir.exists():
        raise FileExistsError(f"{out_dir} already exists; choose an empty --out directory")

    rng = random.Random(seed)
    arms = [("A", arm_a), ("B", arm_b)]
    rng.shuffle(arms)  # which real arm lands in tree_1 vs tree_2 is randomized

    out_dir.mkdir(parents=True)
    mapping = {}
    stripped_by_label = {}
    for label, (real_arm, src) in zip(TREE_LABELS, arms):
        dest = out_dir / label
        stripped = _copy_stripped(src, dest)
        mapping[label] = {"real_arm": real_arm, "source_path": str(src)}
        stripped_by_label[label] = stripped

    manifest = {
        "labels": list(TREE_LABELS),
        "note": "Blinded pairwise judging bundle. Labels do not indicate which arm is which.",
        "stripped": stripped_by_label,
    }
    (out_dir / "MANIFEST.json").write_text(json.dumps(manifest, indent=2))

    key = {
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "seed": seed,
        "bundle_dir": str(out_dir),
        "mapping": mapping,
    }
    key_path = out_dir.parent / f"{out_dir.name}.key.json"
    key_path.write_text(json.dumps(key, indent=2))

    return {
        "bundle_dir": str(out_dir),
        "key_path": str(key_path),
        "labels": list(TREE_LABELS),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--arm-a", required=True, type=Path, help="path to arm A's final project tree")
    parser.add_argument("--arm-b", required=True, type=Path, help="path to arm B's final project tree")
    parser.add_argument("--out", required=True, type=Path, help="destination bundle directory (must not exist)")
    parser.add_argument(
        "--seed",
        type=int,
        default=None,
        help="RNG seed for the A/B->tree_1/tree_2 shuffle (default: nondeterministic)",
    )
    args = parser.parse_args()

    for name, path in (("--arm-a", args.arm_a), ("--arm-b", args.arm_b)):
        if not path.is_dir():
            print(f"error: {name} path does not exist or is not a directory: {path}", file=sys.stderr)
            return 2

    try:
        result = build_bundle(args.arm_a.resolve(), args.arm_b.resolve(), args.out.resolve(), args.seed)
    except FileExistsError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2

    print(json.dumps(result, indent=2))
    print(
        f"\nKeep {result['key_path']} out of what you hand to the judge -- "
        f"only {result['bundle_dir']} should be shared.",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
