# Modules

A module is a shareable, swappable package of workflows, scripts and prompts. A workflow
call names a *slot* (`workflow_name = "claim-task"`), and a `bind` decides which
workflow fills it. Design: [`docs/plans/2026-10-13-modules-design.md`](plans/2026-10-13-modules-design.md).

## Module layout

```
.cloche/modules/github-tasks/
├── module.toml
├── README.md             # required
├── tasks.cloche          # workflows, including the exported ones
├── scripts/
└── prompts/
```

### `module.toml`

```toml
name        = "github-tasks"
description = "Task tracker backed by GitHub Issues (gh)"
exports     = ["list-tasks", "claim-task", "close-task", "release-task", "create-task", "update-task"]  # callable from outside
requires    = ["gh", "jq"]          # host binaries; `cloche validate` and `cloche doctor` check them
credentials = ["GH_TOKEN"]          # secrets expected in the credentials mechanism (warning if unset)
uses        = []                    # slots the module calls but does not provide
```

Workflows not listed in `exports` are private. Module workflows are reachable only
through a bind; they never collide with project workflow names.

### README sections

Every module has a `README.md` with these sections: **Provides** (exported workflows and
their contracts), **Inputs and configuration**, **Prerequisites** (host binaries and
what the container image needs) and **Credentials**. `cloche module add` prints the
Prerequisites section. Cloche never installs a module's prerequisites.

### Paths

`file("prompts/x.md")` in a module `.cloche` file resolves against the module root. Every
`run` command of a module workflow gets `CLOCHE_MODULE_DIR` (host path on the host, the
`/workspace/...` path in a container), so scripts call `"$CLOCHE_MODULE_DIR/scripts/x.sh"`.

## Contracts

```
workflow close-task {
  contract {
    kv:  task_id          # required KV key
    env: GH_TOKEN         # required env var
    out: some_key         # KV key written when the workflow ends in done
  }
  ...
}
```

`contract` is optional. Results are always `success` (reached `done`) and `fail`
(reached `abort`). Inputs are checked before the child starts and `out:` keys when it
ends in `done`; `cloche validate` checks compatibility statically.

## `with`

A `workflow_name` step can remap inputs and outputs:

```
step review {
  workflow_name = "code-review"
  with {
    task_id       from ticket_id          # child kv task_id <- caller kv ticket_id
    env.MODEL     = "opus"                # literal into an env var
    feedback_path from out.review_path    # caller kv <- child's output
  }
  results = [success, fail]
}
```

## Binds

```
bind code-review = ruby-review      # one bind
bind {                              # several
  list-tasks = beads-tasks
}
bind! lint = standardrb-lint        # forced: beats every bind beneath it
```

The right-hand side is a module name (its export named after the slot), `module:workflow`,
or a project workflow. Scope depends on position: top level of `.cloche/project.cloche`
is the project root (and the only scope the daemon's phases use), top level of another
file covers that file's workflows, inside `workflow { }` covers that workflow, inside
`step { }` covers that call. Binds are dynamic: the innermost bind wins across nested
calls, except `bind!`. An unbound slot falls back to a project workflow, then a built-in.

## Task trackers (standard contract)

The daemon's orchestration loop resolves these phases through `project.cloche`:

| Workflow | `kv:` | `out:` | Notes |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | File of task JSONL. A task's optional `repos` field names the repositories its containers need; optional `priority` (0 = most urgent) and `labels` let the daemon spot urgent tasks (priority `0` or an `urgent` label) for `[orchestration] emergency` slots. `beads-tasks` and `github-tasks` don't set it; a project that wants it wraps or replaces `list-tasks`. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Runs before `main`. Lost claim: end in `fail` with `claim_status = unavailable`; the daemon skips the task for the dedup window. |
| `close-task` | `task_id` | — | Also the dashboard's "Close in tracker". |
| `release-task` | `task_id` | — | Also the dashboard's release action. |
| `create-task` | `title` | `new_task_id` | Optional `kv:` inputs: `body`, `priority` (0–4, 0 most urgent), `labels` (comma-separated; priority `0` adds the `urgent` label), `signal_source`, `signal_key`. A source+key is stored on the task (a `signal:<source>:<hash>` label) so `update-task` can find it again. Writes the new task's id as `new_task_id`. |
| `update-task` | `action` | `task_found` | `action = update`: needs `target_task_id`; optional `comment`, `priority`, `add_labels`, `remove_labels`. `action = find`: needs `signal_source` and `signal_key`; writes `task_found` (`true`/`false`) and, when found, the open task's `found_task_id`, so a repeat signal attaches instead of creating. |

`create-task` and `update-task` are for callers outside the loop (signal handling, DRAs).
(These key names avoid `task_id`, which the host executor seeds with the phase's own name.) They are run on demand through the daemon: `cloche tasks create`, `cloche tasks update`
and `cloche tasks find` (HTTP API `POST /api/projects/{name}/tasks/create|update`, which needs the project's
signal token as a bearer token; the CLI reads it from `~/.config/cloche/signal-tokens/`). A `signal_source` input that isn't 1–128 characters of letters, digits, `.`, `_` or `-` is rejected with `400`.

`main` stays project-owned and reads `task_title` / `task_body_path`. This repo ships two
implementations, `.cloche/modules/beads-tasks` and `.cloche/modules/github-tasks`.
Switching trackers is a `project.cloche` edit only:

```
bind {
  list-tasks   = github-tasks      # was beads-tasks
  claim-task   = github-tasks
  close-task   = github-tasks
  release-task = github-tasks
  create-task  = github-tasks
  update-task  = github-tasks
}
```

Transition fallbacks (removed in a future minor release): without `tasks_path` the
daemon parses `list-tasks` stdout; without a resolvable `claim-task` phase, `main` claims
the task itself.

## Asking in Saga: `saga-judge`

`.cloche/modules/saga-judge` lets a workflow ask in Saga and wait, with a judge agent
settling questions that an existing ruling or intent entry already decides. Design:
[`docs/plans/2026-11-10-autonomous-loop-design.md`](plans/2026-11-10-autonomous-loop-design.md), Part 2.

| Workflow | `kv:` | `out:` | Notes |
|---|---|---|---|
| `ask` | `saga_batch_file` | `saga_batch_id` | `saga ask` from a batch file. |
| `await-answer` | `saga_batch_id` | `saga_outcome` | Poll step over `saga poll --no-wait`: `answered`, `reframe` (a question was deprecated by the judge; workflow ends `fail`) or `timeout`. |
| `judge` | `saga_batch_id` | — | Host agent step: decide (citing a ruling or intent id), send back (`--deprecates`) or hold. Holds unless certain; never answers from its own reasoning. Always ends `success`. |

The module's `example-ask-with-judge` wires `ask -> await-answer` and `ask -> judge` in
parallel. It needs a Saga with the judge author kind, the held mark and refuse-if-answered.
See the module README.

## CLI

```
cloche module add <git-url>[@ref] | <path>   # vendor into .cloche/modules/<name>/, record in modules.lock
cloche module list
cloche module update <name>                  # shows the diff of scripts and prompts, asks first
cloche module remove <name>
cloche validate                              # parse, bind, contract, requires and lockfile-hash checks
```

Git modules are pinned to a commit in `.cloche/modules.lock` and committed. Local-path
modules are used in place. Nothing in a module runs at install or update time; review
the diff before merging, as for any agent output.
