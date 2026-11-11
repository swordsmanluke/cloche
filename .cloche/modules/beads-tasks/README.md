# beads-tasks

Task tracker backed by [Beads](https://github.com/steveyegge/beads) (`bd`). Implements the
standard task-management contract; see `docs/modules.md`.

## Provides

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | Writes ready, open, non-epic tasks as JSONL. A task is ready when it carries the `ready` label (`bd update <id> --add-label ready`), `bd` considers it ready, and every closed dependency has a succeeded cloche run. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Sets the task `in_progress`. A closed task sets `claim_status = unavailable` and fails. |
| `close-task` | `task_id` | — | `bd close`. |
| `release-task` | `task_id` | — | Sets the task back to `open`. |
| `create-task` | `title` | `new_task_id` | `bd create`. Optional inputs: `body`, `priority`, `labels`, `signal_source`, `signal_key` (stored as a `signal:<source>:<hash>` label; such a task also gets `ready`). |
| `update-task` | `action` | `task_found` | `action=update` (on `target_task_id`): comment (`bd comments add`), `priority`, `add_labels`/`remove_labels` on `task_id`. `action=find`: the open task labelled for `signal_source`+`signal_key`; sets `task_found` and `found_task_id`. |

All six are host workflows.

## Inputs and configuration

None beyond `task_id`. `claim-task` reads `temp_file_dir` (seeded by the host executor).

## Prerequisites

- `bd` and `jq` on the host `PATH`, and a Beads database in the project.
- `cloche` on the host `PATH` (used for `cloche get|set|list`).

## Credentials

None.
