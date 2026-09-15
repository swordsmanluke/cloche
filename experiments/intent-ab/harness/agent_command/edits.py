"""Parse and apply the file-edit format bonsai is instructed to emit.

bonsai is a weak, small executor, so the edit format is deliberately the
simplest thing that could work: one fenced code block per changed file,
with the file's repo-relative path as the fence's info string and the
file's complete new contents as the block body (whole-file rewrite, no
diffs/patches to get subtly wrong). An opening line may end with " NEW" to
mark a deliberately brand-new file (see EXAMPLE_PLACEHOLDER_PATH and the
no-overlap check in apply_edits — a weak model has echoed the system
prompt's example path verbatim instead of substituting the real target, so
every block is checked against the working directory's actual files):

    ```path/to/existing/file.py
    <complete new file contents>
    ```

    ```path/to/brand/new/file.py NEW
    <complete new file contents>
    ```
"""
import os
import re
from dataclasses import dataclass

_BLOCK_RE = re.compile(
    r"^```([^\n`]+)\n(.*?)\n```[ \t]*$",
    re.MULTILINE | re.DOTALL,
)

# Appended after the path on a fence's opening line to mark a block as a
# brand-new file, e.g. ```newmodule.py NEW — see the no-overlap check in
# apply_edits.
_NEW_MARKER_RE = re.compile(r"^(.*\S)\s+NEW$", re.IGNORECASE)

# Stand-in path used in the system prompt's example fence (see
# cli.build_system_prompt). Deliberately not plausible-looking, so a weak
# model that echoes the example verbatim instead of substituting the real
# target path produces something apply_edits can recognize and reject,
# rather than a silent no-op write to a bogus-but-real-looking path.
EXAMPLE_PLACEHOLDER_PATH = "REPLACE-WITH-THE-REAL-PATH-YOU-ARE-EDITING.py"


@dataclass
class EditBlock:
    path: str
    content: str
    is_new: bool = False


class UnsafePathError(ValueError):
    """Raised when a proposed edit path would escape the working directory."""


class PlaceholderPathError(ValueError):
    """Raised when a block's path is the system prompt's example placeholder."""


class NoExistingFileOverlapError(ValueError):
    """Raised when no edit block targets an existing file and none is marked NEW."""


def _parse_header(header: str):
    """Split a fence header into (path, is_new), stripping a trailing NEW marker."""
    header = header.strip()
    match = _NEW_MARKER_RE.match(header)
    if match:
        return match.group(1).strip(), True
    return header, False


def parse_edit_blocks(text: str) -> list:
    """Extract (path, content) edit blocks from a model completion."""
    blocks = []
    for match in _BLOCK_RE.finditer(text):
        path, is_new = _parse_header(match.group(1))
        # Skip language-only fences (e.g. ```python) with no path-like info
        # string — a bare word with no separators and a common language name
        # is almost certainly a syntax-highlighted snippet, not a target file.
        if "/" not in path and "." not in path and "\\" not in path:
            continue
        blocks.append(EditBlock(path=path, content=match.group(2), is_new=is_new))
    return blocks


def list_workdir_files(workdir: str) -> list:
    """Return sorted paths, relative to workdir, of files that currently exist
    there (skipping VCS metadata) — used to show the model the exact paths it
    is allowed to reference instead of a guessed or example one."""
    paths = []
    for root, dirs, files in os.walk(workdir):
        dirs[:] = [d for d in dirs if d != ".git"]
        for name in files:
            rel = os.path.relpath(os.path.join(root, name), workdir)
            paths.append(rel.replace(os.sep, "/"))
    return sorted(paths)


def resolve_safe_path(workdir: str, rel_path: str) -> str:
    """Resolve rel_path under workdir, rejecting any escape (.., absolute paths)."""
    workdir_real = os.path.realpath(workdir)
    candidate = os.path.realpath(os.path.join(workdir_real, rel_path))
    if candidate != workdir_real and not candidate.startswith(workdir_real + os.sep):
        raise UnsafePathError(f"edit path {rel_path!r} escapes working directory")
    return candidate


def apply_edits(workdir: str, blocks: list) -> list:
    """Write each edit block's content to its path under workdir.

    Returns the list of relative paths actually written. Raises
    UnsafePathError before writing anything if any block's path escapes
    workdir, PlaceholderPathError if any block still carries the system
    prompt's example path, and NoExistingFileOverlapError if none of the
    blocks target an existing file and none is marked NEW — so a single bad
    block can't cause partial application, and a model that echoed the
    format example (or invented an unrelated path) instead of editing the
    real target fails loudly rather than silently no-op-succeeding.
    """
    for block in blocks:
        if EXAMPLE_PLACEHOLDER_PATH in block.path:
            raise PlaceholderPathError(
                f"edit path {block.path!r} contains the system prompt's example "
                "placeholder path, not a real target file"
            )

    targets = [(block, resolve_safe_path(workdir, block.path)) for block in blocks]

    if targets and not any(
        block.is_new or os.path.exists(abs_path) for block, abs_path in targets
    ):
        raise NoExistingFileOverlapError(
            "none of the proposed edit paths match an existing file in the "
            "working directory, and none is marked as a new file (NEW)"
        )

    written = []
    for block, abs_path in targets:
        os.makedirs(os.path.dirname(abs_path), exist_ok=True)
        content = block.content
        if not content.endswith("\n"):
            content += "\n"
        with open(abs_path, "w") as f:
            f.write(content)
        written.append(block.path)
    return written
