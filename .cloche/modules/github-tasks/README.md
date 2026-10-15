# github-tasks

Task tracker backed by GitHub Issues through the [`gh`](https://cli.github.com) CLI.
Implements the same contract as `beads-tasks`; swap between them in
`.cloche/project.cloche`.

## Provides

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | Writes open issues without the claim label as JSONL (`id` is the issue number). |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Adds the claim label. A closed issue sets `claim_status = unavailable` and fails. |
| `close-task` | `task_id` | — | Removes the claim label and closes the issue. |
| `release-task` | `task_id` | — | Removes the claim label. |

All four are host workflows. Issue dependencies are not modelled: every open, unclaimed
issue is ready.

## Inputs and configuration

| Env var | Default | Meaning |
|---|---|---|
| `CLOCHE_GH_CLAIM_LABEL` | `cloche:in-progress` | Label marking a claimed issue. Created on first claim. |
| `GH_REPO` | repo of the project checkout | Repository to use, as understood by `gh`. |

## Prerequisites

- `gh` and `jq` on the host `PATH`.
- `cloche` on the host `PATH`.

## Credentials

`gh` must be authenticated: run `gh auth login`, or provide `GH_TOKEN` to the daemon's
environment (never commit it). The token needs `issues: write`.
