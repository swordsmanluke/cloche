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
exports     = ["list-tasks", "claim-task", "close-task", "release-task"]  # callable from outside
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
| `list-tasks` | — | `tasks_path` | File of task JSONL. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Runs before `main`. Lost claim: end in `fail` with `claim_status = unavailable`; the daemon skips the task for the dedup window. |
| `close-task` | `task_id` | — | Also the dashboard's "Close in tracker". |
| `release-task` | `task_id` | — | Also the dashboard's release action. |

`main` stays project-owned and reads `task_title` / `task_body_path`. This repo ships two
implementations, `.cloche/modules/beads-tasks` and `.cloche/modules/github-tasks`.
Switching trackers is a `project.cloche` edit only:

```
bind {
  list-tasks   = github-tasks      # was beads-tasks
  claim-task   = github-tasks
  close-task   = github-tasks
  release-task = github-tasks
}
```

Transition fallbacks (removed in a future minor release): without `tasks_path` the
daemon parses `list-tasks` stdout; without a resolvable `claim-task` phase, `main` claims
the task itself.

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
