"""Parse and apply the file-edit format bonsai is instructed to emit.

bonsai is a weak, small executor, so the edit format is deliberately the
simplest thing that could work: one fenced code block per changed file,
with the file's repo-relative path as the fence's info string and the
file's complete new contents as the block body (whole-file rewrite, no
diffs/patches to get subtly wrong).

    ```path/to/file.py
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


@dataclass
class EditBlock:
    path: str
    content: str


class UnsafePathError(ValueError):
    """Raised when a proposed edit path would escape the working directory."""


def parse_edit_blocks(text: str) -> list:
    """Extract (path, content) edit blocks from a model completion."""
    blocks = []
    for match in _BLOCK_RE.finditer(text):
        path = match.group(1).strip()
        # Skip language-only fences (e.g. ```python) with no path-like info
        # string — a bare word with no separators and a common language name
        # is almost certainly a syntax-highlighted snippet, not a target file.
        if "/" not in path and "." not in path and "\\" not in path:
            continue
        blocks.append(EditBlock(path=path, content=match.group(2)))
    return blocks


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
    workdir, so a single bad block can't cause partial application.
    """
    targets = [(block, resolve_safe_path(workdir, block.path)) for block in blocks]
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
