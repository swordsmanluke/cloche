# beads-tasks

Task tracker backed by [Beads](https://github.com/steveyegge/beads) (`bd`). Implements the
standard task-management contract; see `docs/modules.md`.

## Provides

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | Writes ready, open, non-epic tasks as JSONL. A task is ready when `bd` considers it ready and every closed dependency has a succeeded cloche run. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Sets the task `in_progress`. A closed task sets `claim_status = unavailable` and fails. |
| `close-task` | `task_id` | — | `bd close`. |
| `release-task` | `task_id` | — | Sets the task back to `open`. |

All four are host workflows.

## Inputs and configuration

None beyond `task_id`. `claim-task` reads `temp_file_dir` (seeded by the host executor).

## Prerequisites

- `bd` and `jq` on the host `PATH`, and a Beads database in the project.
- `cloche` on the host `PATH` (used for `cloche get|set|list`).

## Credentials

None.
