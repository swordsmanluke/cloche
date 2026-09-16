"""Thin wrappers around the `cloche` and `bd` CLIs the driver shells out to.

Kept to the minimal command surface run_arm.py actually needs (see
../arms/README.md and docs/USAGE.md's CLI reference for the full contract
of each command used here):

  bd init --non-interactive
  bd create --graph <file>
  bd ready --json                                  (also the exhaustion check —
                                                      it lists open AND
                                                      in_progress tasks, so an
                                                      empty result means fully
                                                      closed, not just "nothing
                                                      claimable right now")
  cloche init --non-interactive
  cloche loop / cloche loop stop
  cloche activity --json --project <dir>
  cloche status <task-id>                          (best-effort token scrape)
  cloche intent preview --workflow <wf> --step <s> --project <dir>
  cloche get <key>  (with CLOCHE_TASK_ID set)       (best-effort KV read)

Every method raises `CLIError` on an unexpected failure except the ones
documented "best-effort", which return `None` instead — matching how
`cloche status`'s Tokens line and `cloche intent preview`'s selection are
themselves documented as omitted/approximate when there's nothing to show.
"""
import json
import os
import subprocess
from pathlib import Path


class CLIError(RuntimeError):
    pass


class Toolchain:
    def __init__(self, project_dir: Path, extra_env: dict = None):
        self.project_dir = Path(project_dir)
        self.extra_env = extra_env or {}

    def _run(self, args, cwd=None, check=True, env_overrides=None):
        env = dict(os.environ)
        env.update(self.extra_env)
        if env_overrides:
            env.update(env_overrides)
        result = subprocess.run(
            args, cwd=str(cwd or self.project_dir), env=env,
            capture_output=True, text=True,
        )
        if check and result.returncode != 0:
            raise CLIError(f"{' '.join(args)} failed ({result.returncode}): {result.stderr.strip()}")
        return result

    # -- bd (task tracker) ------------------------------------------------

    def bd_init(self):
        self._run(["bd", "init", "--quiet", "--skip-hooks"])

    def bd_create_graph(self, task_list_path: Path):
        # The installed bd has no --graph bulk mode; create nodes one at a
        # time with explicit ids, then wire "blocks" edges via bd dep.
        # Node keys become issue ids under the project prefix.
        graph = json.loads(task_list_path.read_text())
        ids = {}
        for node in graph["nodes"]:
            result = self._run([
                "bd", "create", f"{node['key']}: {node['title']}",
                "--type", node.get("type", "task"),
                "-d", node.get("description", ""),
                "--silent",
            ])
            ids[node["key"]] = result.stdout.strip().splitlines()[-1]
        for edge in graph.get("edges", []):
            if edge.get("type") != "blocks":
                continue
            # from_key depends on to_key (to_key blocks from_key)
            self._run(["bd", "dep", "add", ids[edge["from_key"]], ids[edge["to_key"]]])

    def bd_ready(self) -> list:
        result = self._run(["bd", "ready", "--json"])
        return json.loads(result.stdout or "[]")

    # -- cloche (project registration + loop) ------------------------------

    def cloche_init(self):
        self._run(["cloche", "init", "--non-interactive"])

    def loop_start(self):
        self._run(["cloche", "loop"])

    def loop_stop(self):
        self._run(["cloche", "loop", "stop"])

    # -- metrics ------------------------------------------------------------

    def activity_json(self) -> list:
        result = self._run(["cloche", "activity", "--json", "--project", str(self.project_dir)])
        entries = []
        for line in result.stdout.splitlines():
            line = line.strip()
            if line:
                entries.append(json.loads(line))
        return entries

    def status_text(self, task_id: str):
        """Best-effort: returns None (rather than raising) on failure, since
        a task with no usage data yet is a normal, not exceptional, state."""
        result = self._run(["cloche", "status", task_id], check=False)
        return result.stdout if result.returncode == 0 else None

    def intent_preview(self, workflow: str, step: str):
        """Best-effort: arm-B-only context-composition metric. Returns None
        on failure (e.g. no requirements selected yet)."""
        result = self._run(
            ["cloche", "intent", "preview", "--workflow", workflow, "--step", step,
             "--project", str(self.project_dir)],
            check=False,
        )
        return result.stdout if result.returncode == 0 else None

    def kv_get(self, task_id: str, key: str):
        """Best-effort: returns None if the key was never set for this task."""
        result = self._run(["cloche", "get", key], check=False, env_overrides={"CLOCHE_TASK_ID": task_id})
        return result.stdout.strip() if result.returncode == 0 else None
