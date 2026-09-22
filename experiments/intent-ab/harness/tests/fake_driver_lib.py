"""Shared state machine for tests/fake_bin/{bd,cloche} -- fake CLI
stand-ins used by test_driver_integration.py to prove arm_driver.run_arm
drives a project to task-list exhaustion end-to-end without a real cloche
daemon/Docker/bd (unavailable in this sandbox -- the same constraint E4
worked around by faking Ollama; see harness/README.md).

State lives at <cwd>/.fake_driver_state.json. Both fakes are always
invoked with cwd=<arm target dir> (arm_driver.cloche_cli.Toolchain runs
every command with cwd=self.project_dir), so this naturally scopes one
state machine per arm run with no coordination needed between the two
fake executables.
"""
import json
from datetime import datetime, timezone
from pathlib import Path

STATE_FILENAME = ".fake_driver_state.json"

_EMPTY_STATE = {
    "order": [], "titles": {}, "phase": {}, "closed": [],
    "attempt_id": {}, "attempt_counter": 0, "activity": [],
}


def state_path() -> Path:
    return Path.cwd() / STATE_FILENAME


def load() -> dict:
    path = state_path()
    if not path.exists():
        return dict(_EMPTY_STATE)
    return json.loads(path.read_text())


def save(state: dict) -> None:
    state_path().write_text(json.dumps(state))


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _record(driver_state: dict, **entry) -> None:
    driver_state["activity"].append({"ts": _now(), **entry})


def _topo_order(nodes: dict, edges: list) -> list:
    """Edge type "blocks": from_key depends on to_key, same convention as
    seed/.cloche/tasks/seed-tasks.json."""
    deps = {key: [] for key in nodes}
    for edge in edges:
        if edge.get("type", "blocks") == "blocks":
            deps[edge["from_key"]].append(edge["to_key"])

    order = []
    resolved = set()
    remaining = set(nodes)
    while remaining:
        ready_now = [k for k in remaining if all(d in resolved for d in deps[k])]
        if not ready_now:
            raise ValueError("cyclic or unsatisfiable task graph")
        for key in sorted(ready_now):
            order.append(key)
            resolved.add(key)
            remaining.discard(key)
    return order


def _install_graph(state: dict, nodes: dict, edges: list) -> None:
    order = _topo_order(nodes, edges)
    state["order"] = order
    state["titles"] = {key: nodes[key].get("title", key) for key in order}
    state["phase"] = {key: state.get("phase", {}).get(key, 0) for key in order}
    state.setdefault("closed", [])


def create_graph(graph_path) -> None:
    """Simulates the old `bd create --graph <file>` bulk form."""
    graph = json.loads(Path(graph_path).read_text())
    nodes = {n["key"]: n for n in graph["nodes"]}
    state = load()
    state["closed"] = []
    _install_graph(state, nodes, graph.get("edges", []))
    save(state)


def add_node(key: str, title: str) -> str:
    """Simulates `bd create "<key>: <title>" ... --silent` (the form
    arm_driver.cloche_cli.Toolchain.bd_create_graph uses): the node's id
    is its key. Returns the id, which the fake prints."""
    state = load()
    nodes = dict(state.get("nodes", {}))
    nodes[key] = {"key": key, "title": title}
    state["nodes"] = nodes
    _install_graph(state, nodes, state.get("edges", []))
    save(state)
    return key


def add_edge(from_key: str, to_key: str) -> None:
    """Simulates `bd dep add <from> <to>` (from depends on to)."""
    state = load()
    edges = list(state.get("edges", []))
    edges.append({"type": "blocks", "from_key": from_key, "to_key": to_key})
    state["edges"] = edges
    _install_graph(state, state.get("nodes", {}), edges)
    save(state)


def _active_key(state: dict):
    for key in state["order"]:
        if key not in state["closed"]:
            return key
    return None


def tick_ready(state: dict) -> list:
    """Advances the fake's simulated background work by one tick (called
    once per `bd ready --json` invocation) and returns that call's
    response. Two ticks per task: tick N claims+starts it (phase 0->1),
    tick N+1 runs it to completion and closes it (phase 1->2), revealing
    the next task in the chain for the following call."""
    key = _active_key(state)
    if key is not None:
        phase = state["phase"][key]
        if phase == 0:
            state["phase"][key] = 1
            state["attempt_counter"] += 1
            attempt_id = f"a{state['attempt_counter']}"
            state["attempt_id"][key] = attempt_id
            _record(state, kind="attempt_started", task_id=key, attempt_id=attempt_id, workflow="develop")
            _record(state, kind="step_started", task_id=key, attempt_id=attempt_id, workflow="develop", step="implement")
        elif phase == 1:
            attempt_id = state["attempt_id"][key]
            _record(state, kind="step_completed", task_id=key, attempt_id=attempt_id, workflow="develop", step="implement", result="success")
            for step in ("commit", "test", "merge"):
                _record(state, kind="step_started", task_id=key, attempt_id=attempt_id, workflow="develop", step=step)
                _record(state, kind="step_completed", task_id=key, attempt_id=attempt_id, workflow="develop", step=step, result="success")
            _record(state, kind="attempt_ended", task_id=key, attempt_id=attempt_id, state="succeeded")
            state["closed"].append(key)
            state["phase"][key] = 2
            key = _active_key(state)

    response = []
    if key is not None:
        status = "open" if state["phase"][key] == 0 else "in_progress"
        response.append({"id": key, "title": state["titles"][key], "status": status, "issue_type": "task"})
    save(state)
    return response


def token_count_for(state: dict, task_id: str):
    if task_id not in state["closed"]:
        return None
    return 1000 + state["order"].index(task_id) * 111
