"""Extract a frozen ref of the seed project into a standalone directory.

The seed repo (experiments/intent-ab/seed/, E1) currently lives as a subtree
of this monorepo rather than a split-out repository, so "clone seed at tag
seed-v1" means: export that subtree's content at the tagged commit with
`git archive`, then `git init` a fresh repository at the target so Cloche
has a normal git history to branch/worktree from (`cloche run` requires a
git repository — docs/USAGE.md).

If the seed is later split into its own repository, this still works
unchanged: `source` would then be a repo root itself, the "subtree path" is
simply empty, and `git archive` exports the whole tree.
"""
import io
import os
import subprocess
import tarfile
from pathlib import Path


class SeedCloneError(RuntimeError):
    pass


def _run_git(args, **kwargs):
    return subprocess.run(["git", *args], **kwargs)


def _repo_root(path: Path) -> Path:
    result = _run_git(
        ["rev-parse", "--show-toplevel"], cwd=str(path), capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SeedCloneError(f"{path} is not inside a git repository: {result.stderr.strip()}")
    return Path(result.stdout.strip())


def clone_seed(source: Path, ref: str, target_dir: Path) -> None:
    """Extract `source` (a directory inside some git repo) as it existed at
    `ref`, into a fresh standalone git repository at `target_dir`.

    `target_dir` must not already exist.
    """
    source = Path(source).resolve()
    target_dir = Path(target_dir)
    if target_dir.exists():
        raise SeedCloneError(f"target directory already exists: {target_dir}")

    repo_root = _repo_root(source)
    rel = os.path.relpath(str(source), str(repo_root))

    archive_args = ["archive", ref]
    if rel != ".":
        archive_args += ["--", rel]
    result = _run_git(archive_args, cwd=str(repo_root), capture_output=True)
    if result.returncode != 0:
        raise SeedCloneError(
            f"git archive {ref} -- {rel} failed: {result.stderr.decode(errors='replace').strip()}"
        )

    target_dir.mkdir(parents=True)
    prefix = f"{rel}/" if rel != "." else ""
    with tarfile.open(fileobj=io.BytesIO(result.stdout)) as tar:
        for member in tar.getmembers():
            if prefix:
                if not member.name.startswith(prefix):
                    continue
                member.name = member.name[len(prefix):]
            if member.name in ("", "."):
                continue
            tar.extract(member, path=str(target_dir))

    if not any(target_dir.iterdir()):
        raise SeedCloneError(f"git archive {ref} -- {rel} produced no files (bad ref or path?)")

    _init_commit(target_dir, ref)


def _init_commit(target_dir: Path, ref: str) -> None:
    env = dict(os.environ)
    name = env.get("CLOCHE_GIT_AUTHOR_NAME") or "cloche"
    email = env.get("CLOCHE_GIT_AUTHOR_EMAIL") or "cloche@local"
    env.update({
        "GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
        "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email,
    })

    def git(args):
        r = _run_git(["-C", str(target_dir), *args], env=env, capture_output=True, text=True)
        if r.returncode != 0:
            raise SeedCloneError(f"git {' '.join(args)} failed: {r.stderr.strip()}")
        return r

    git(["init", "-q"])
    git(["add", "-A"])
    git(["commit", "-q", "--allow-empty", "-m", f"Seed checkout ({ref})"])
