"""End-to-end tests for the agent_command wrapper against the fixture repo.

Exercises the same path Cloche would: a full assembled prompt on stdin, a
nonce in CLOCHE_RESULT_NONCE, edits applied to a working directory, and a
nonced CLOCHE_RESULT marker on stdout. Ollama is faked (see fake_ollama.py)
since no real Ollama/bonsai is available in this environment; the fake
server's responses are shaped like real bonsai output (leaked
chain-of-thought included) to exercise the full pipeline.

The "three times in a row" loop below is this repo's stand-in for E4's
acceptance bar ("from inside a cloche container, the wrapper completes a
trivial scripted edit task against a fixture repo three times in a row") —
a real container+Ollama run is a manual step (see harness/README.md); this
proves the wrapper's logic is deterministic and repeatable given a
same-shaped model response.
"""
import io
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from agent_command.cli import run
from agent_command.ollama_client import OllamaClient
from tests.fake_ollama import FakeOllamaServer

HARNESS_ROOT = Path(__file__).resolve().parent.parent
FIXTURE_REPO = HARNESS_ROOT / "fixture" / "repo"
FIXTURE_PROMPT = (HARNESS_ROOT / "fixture" / "task_prompt.md").read_text()
NONCE = "testnonce123"

FIXED_CALC_PY = "def add(a, b):\n    return a + b\n"

BONSAI_STYLE_RESPONSE = (
    "<think>\n"
    "The user wants add() to return a sum instead of a difference. Let me\n"
    "rewrite the file.\n"
    "</think>\n"
    f"```calc.py\n{FIXED_CALC_PY}```\n"
    f"CLOCHE_RESULT:{NONCE}:success\n"
)


def _fresh_workdir():
    tmp = tempfile.mkdtemp()
    shutil.copytree(FIXTURE_REPO, tmp, dirs_exist_ok=True)
    return tmp


def _calc_result(workdir):
    """Import the fixture's calc.py from workdir and call add(2, 3)."""
    ns = {}
    exec((Path(workdir) / "calc.py").read_text(), ns)
    return ns["add"](2, 3)


class TestRunAppliesEditsAndReportsResult(unittest.TestCase):
    def test_success_path_fixes_the_bug(self):
        workdir = _fresh_workdir()
        with FakeOllamaServer([BONSAI_STYLE_RESPONSE]) as server:
            client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
            out = io.StringIO()
            code = run(FIXTURE_PROMPT.format(nonce=NONCE), NONCE, workdir, client, out=out)
        self.assertEqual(code, 0)
        self.assertEqual(out.getvalue().strip(), f"CLOCHE_RESULT:{NONCE}:success")
        self.assertEqual(_calc_result(workdir), 5)

    def test_three_times_in_a_row(self):
        # The acceptance bar from the ticket, run deterministically against
        # the fake endpoint: fresh working dir each time, must succeed all
        # three times.
        for attempt in range(3):
            workdir = _fresh_workdir()
            nonce = f"nonce-{attempt}"
            response = BONSAI_STYLE_RESPONSE.replace(NONCE, nonce)
            with FakeOllamaServer([response]) as server:
                client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
                out = io.StringIO()
                code = run(FIXTURE_PROMPT.format(nonce=nonce), nonce, workdir, client, out=out)
            self.assertEqual(code, 0, f"attempt {attempt} exited {code}")
            self.assertEqual(out.getvalue().strip(), f"CLOCHE_RESULT:{nonce}:success")
            self.assertEqual(_calc_result(workdir), 5, f"attempt {attempt} did not fix the bug")

    def test_retry_on_empty_then_succeeds(self):
        workdir = _fresh_workdir()
        with FakeOllamaServer(["", BONSAI_STYLE_RESPONSE]) as server:
            client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
            out = io.StringIO()
            code = run(FIXTURE_PROMPT.format(nonce=NONCE), NONCE, workdir, client, out=out)
        self.assertEqual(code, 0)
        self.assertEqual(len(server.requests), 2)
        self.assertEqual(out.getvalue().strip(), f"CLOCHE_RESULT:{NONCE}:success")
        self.assertEqual(_calc_result(workdir), 5)

    def test_persistently_empty_content_reports_fail(self):
        workdir = _fresh_workdir()
        original = (Path(workdir) / "calc.py").read_text()
        with FakeOllamaServer(["", ""]) as server:
            client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
            out = io.StringIO()
            code = run(FIXTURE_PROMPT.format(nonce=NONCE), NONCE, workdir, client, out=out)
        self.assertEqual(code, 0)
        self.assertEqual(out.getvalue().strip(), f"CLOCHE_RESULT:{NONCE}:fail")
        # No edits should have been attempted.
        self.assertEqual((Path(workdir) / "calc.py").read_text(), original)

    def test_bonsai_never_receives_zero_temperature(self):
        workdir = _fresh_workdir()
        with FakeOllamaServer([BONSAI_STYLE_RESPONSE]) as server:
            client = OllamaClient(base_url=server.base_url, temperature=0, timeout_seconds=5)
            run(FIXTURE_PROMPT.format(nonce=NONCE), NONCE, workdir, client, out=io.StringIO())
            self.assertGreater(server.requests[0]["temperature"], 0)


class TestExecutableEndToEnd(unittest.TestCase):
    """Invokes bin/agent_command as a real subprocess, the way Cloche does."""

    def test_subprocess_invocation(self):
        workdir = _fresh_workdir()
        with FakeOllamaServer([BONSAI_STYLE_RESPONSE]) as server:
            env = dict(os.environ)
            env["CLOCHE_RESULT_NONCE"] = NONCE
            env["BONSAI_BASE_URL"] = server.base_url
            env["BONSAI_TIMEOUT_SECONDS"] = "5"
            proc = subprocess.run(
                [sys.executable, str(HARNESS_ROOT / "bin" / "agent_command")],
                input=FIXTURE_PROMPT.format(nonce=NONCE),
                capture_output=True,
                text=True,
                cwd=workdir,
                env=env,
                timeout=15,
            )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(f"CLOCHE_RESULT:{NONCE}:success", proc.stdout)
        self.assertEqual(_calc_result(workdir), 5)


if __name__ == "__main__":
    unittest.main()
