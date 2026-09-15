"""agent_command entry point.

Contract (docs/built-in-agents.md "Unknown Agents", docs/USAGE.md "Result
Protocol"): Cloche invokes this as an `agent_command` for a prompt step. It
receives the fully-assembled prompt on stdin, `CLOCHE_RESULT_NONCE` in the
environment, and must print a `CLOCHE_RESULT:<nonce>:<name>` line to stdout
naming one of the step's declared results — the classifier ignores exit
code once a marker naming this run's nonce is present.

`-c` / `--resume` are accepted and ignored: this wrapper is stateless
(bonsai calls carry no server-side session), so a resume/recovery
invocation is handled identically to a fresh one — read whatever prompt
arrives on stdin (a full task prompt, or the short "print the marker"
recovery prompt) and answer it the same way.
"""
import argparse
import os
import re
import sys

from . import cot, edits
from .ollama_client import (
    DEFAULT_BASE_URL,
    DEFAULT_MAX_TOKENS,
    DEFAULT_MODEL,
    DEFAULT_TEMPERATURE,
    DEFAULT_TIMEOUT_SECONDS,
    OllamaClient,
    OllamaError,
)

SYSTEM_PROMPT = """You are a coding assistant that edits files by rewriting them whole.

To change a file, output a fenced code block whose opening line is three
backticks immediately followed by the file's path relative to the project
root (no space, no language tag), then the file's COMPLETE new contents,
then a closing fenced line of three backticks. Example:

```path/to/file.py
def add(a, b):
    return a + b
```

Output one such block per file you change, and nothing else inside the
blocks but real file contents. After your edit block(s), find the exact
result marker line the task gave you to report completion (it looks like
"CLOCHE_RESULT:<something>:<name>") and print that line, verbatim and
unmodified, on its own line at the very end of your reply. Print nothing
else after it."""


def _marker_pattern(nonce: str) -> "re.Pattern":
    if nonce:
        return re.compile(r"^CLOCHE_RESULT:" + re.escape(nonce) + r":(\S+)$", re.MULTILINE)
    return re.compile(r"^CLOCHE_RESULT:(\S+)$", re.MULTILINE)


def find_marker(text: str, nonce: str):
    match = _marker_pattern(nonce).search(text)
    return match.group(0) if match else None


def declared_result_names(prompt: str, nonce: str) -> list:
    """Learn which result names the step actually declared, from the
    "## Result Selection" block Cloche appends to the prompt, so a
    synthesized fallback marker (see run()) never invents a name the step
    doesn't recognize."""
    pattern = re.compile(r"^CLOCHE_RESULT:" + re.escape(nonce) + r":(\S+)$", re.MULTILINE)
    return pattern.findall(prompt) if nonce else []


def _env_float(name: str, default: float) -> float:
    raw = os.environ.get(name)
    return float(raw) if raw else default


def _env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    return int(raw) if raw else default


def build_client_from_env() -> OllamaClient:
    return OllamaClient(
        base_url=os.environ.get("BONSAI_BASE_URL", DEFAULT_BASE_URL),
        model=os.environ.get("BONSAI_MODEL", DEFAULT_MODEL),
        temperature=_env_float("BONSAI_TEMPERATURE", DEFAULT_TEMPERATURE),
        max_tokens=_env_int("BONSAI_MAX_TOKENS", DEFAULT_MAX_TOKENS),
        timeout_seconds=_env_float("BONSAI_TIMEOUT_SECONDS", DEFAULT_TIMEOUT_SECONDS),
    )


def parse_args(argv):
    parser = argparse.ArgumentParser(
        prog="agent_command",
        description="bonsai-8b-16k executor wrapper (agent_command for Cloche prompt steps)",
    )
    parser.add_argument("-c", "--resume", action="store_true", help="accepted, ignored (stateless wrapper)")
    return parser.parse_known_args(argv)[0]


def run(prompt: str, nonce: str, workdir: str, client: OllamaClient, out=sys.stdout, err=sys.stderr) -> int:
    def is_empty(raw_content: str) -> bool:
        return cot.strip_chain_of_thought(raw_content) == ""

    try:
        raw_content = client.complete_with_retry(SYSTEM_PROMPT, prompt, is_empty)
    except OllamaError as e:
        print(f"agent_command: {e}", file=err)
        raw_content = ""

    clean_content = cot.strip_chain_of_thought(raw_content)

    if clean_content == "":
        print("agent_command: empty content after retry", file=err)
        print(_fallback_marker(prompt, nonce, "fail"), file=out)
        return 0

    applied = []
    try:
        blocks = edits.parse_edit_blocks(clean_content)
        applied = edits.apply_edits(workdir, blocks)
    except edits.UnsafePathError as e:
        print(f"agent_command: {e}", file=err)
        print(_fallback_marker(prompt, nonce, "fail"), file=out)
        return 0

    print(f"agent_command: applied {len(applied)} edit(s): {', '.join(applied)}", file=err)

    marker = find_marker(clean_content, nonce)
    if marker is None:
        marker = _fallback_marker(prompt, nonce, "success" if applied else "fail")
    print(marker, file=out)
    return 0


def _fallback_marker(prompt: str, nonce: str, preferred: str) -> str:
    """Best-effort marker when the model didn't echo one back.

    Cloche's own engine also retries a missing marker with a recovery turn,
    so this is a secondary safety net, not the primary path — prefer a
    name the step actually declared over inventing one.
    """
    if not nonce:
        return f"CLOCHE_RESULT:{preferred}"
    names = declared_result_names(prompt, nonce)
    if preferred in names:
        return f"CLOCHE_RESULT:{nonce}:{preferred}"
    if names:
        return f"CLOCHE_RESULT:{nonce}:{names[0]}"
    return f"CLOCHE_RESULT:{nonce}:{preferred}"


def main(argv=None) -> int:
    parse_args(sys.argv[1:] if argv is None else argv)
    prompt = sys.stdin.read()
    nonce = os.environ.get("CLOCHE_RESULT_NONCE", "")
    workdir = os.getcwd()
    client = build_client_from_env()
    return run(prompt, nonce, workdir, client)


if __name__ == "__main__":
    sys.exit(main())
