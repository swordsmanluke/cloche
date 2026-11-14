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

### Group binds

A group bind routes every slot in a built-in group to one module's same-named export:

```
bind qa = saga       # ask, await-answer, read-answer
bind tasks = beads-tasks   # list-tasks, claim-task, close-task, release-task, create-task, update-task
```

`qa` and `tasks` are reserved group names: a project workflow or module export may not
use them as a slot name. A per-slot bind in the same scope beats a group bind.

### Bind settings

A bind may carry literal environment settings that reach the slot's run:

```
bind qa = saga { env.SAGA_PROJECT = "cloche" }
```

For a group bind the settings apply to every slot in the group. The settings come from
the bind that wins resolution, so an inner-scope bind's settings replace an outer one's.
A bind setting satisfies a contract `env:` requirement. Env precedence, lowest to highest:
the daemon's environment, an enclosing call's env, the winning bind's settings, then the
step's own `with` block.

> **Credentials:** bind settings are literal text in a checked-in `.cloche` file. Do not
> put secrets there; `cloche validate` warns when a bind sets a variable the module lists
> as a credential. Keep credentials in the daemon's environment.

## Task trackers (standard contract)

The daemon's orchestration loop resolves these phases through `project.cloche`:

| Workflow | `kv:` | `out:` | Notes |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | File of task JSONL. A task's optional `repos` field names the repositories its containers need; optional `priority` (0 = most urgent) and `labels` let the daemon spot urgent tasks (priority `0` or an `urgent` label) for `[orchestration] emergency` slots. `beads-tasks` and `github-tasks` don't set it; a project that wants it wraps or replaces `list-tasks`. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | Runs before `main`. Lost claim: end in `fail` with `claim_status = unavailable`; the daemon skips the task for the dedup window. |
| `close-task` | `task_id` | — | Also the dashboard's "Close in tracker". |
| `release-task` | `task_id` | — | Also the dashboard's release action. |
| `create-task` | `title` | `new_task_id` | Optional `kv:` inputs: `body`, `priority` (0–4, 0 most urgent), `labels` (comma-separated; priority `0` adds the `urgent` label), `signal_source`, `signal_key`. A source+key is stored on the task (a `signal:<source>:<hash>` label) so `update-task` can find it again. Writes the new task's id as `new_task_id`. |
| `update-task` | `action` | `task_found` | `action = update`: needs `target_task_id`; optional `comment`, `priority`, `add_labels`, `remove_labels`. `action = find`: needs `signal_source` and `signal_key`; writes `task_found` (`true`/`false`) and, when found, the open task's `found_task_id`, so a repeat signal attaches instead of creating. `action = get`: needs `target_task_id`; read-only; writes `task_found` and, when found, `task_labels` (comma-separated) and `task_status` (the daemon uses it to check a DRA's domain label). |

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

## Questions (the `qa` contract)

`bind qa = <module>` fills three slots. `cloche questions` and the daemon's
`POST /api/projects/{name}/questions/ask|await|read` routes (project signal token required)
run them as host workflows with the bind's env settings.

| Slot | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `ask` | `questions_file` | `question_ids` | `questions_file` is a JSON file `{title, context, questions: [{prompt, detail, options}], backend}` (`backend` is opaque to cloche). Creates one question per entry; `question_ids` is one id per line, in order. |
| `await-answer` | `question_ids` | `questions_open` | **Non-blocking.** `question_ids` is one id per line; `questions_open` lists those with no answer (empty when all are answered). |
| `read-answer` | `question_id` | `answer_path` | Path to a file holding the answer text. **Ends `fail` when there is no answer.** When a human and a judge both answered, it returns the human's. |

The module may also ship `asking.md` (how to ask well) and `judge.md` (how a judge should
answer) at its root; `cloche questions guide [--judge]` serves them. `cloche validate` warns when
a module bound to `qa` has no `judge.md`, since the judge has nothing to follow for it. With no module bound
to `qa`, `ask` fails with "no qa backend is bound ... add `bind qa = <module>`".

### The judge

After a successful `cloche questions ask`, the daemon starts one run of the built-in host workflow
`judge` for the new ids. Its prompt is cloche's generic rules, then the bound module's `judge.md`,
then the ids to judge. The rules: cite sources; never replace a human answer; leave a question
unanswered when unsure; state a false premise as the answer; leave permission requests unanswered
unless a prior ruling you can cite covers them. `judge.md` is where a module says how to read and
record answers in its tracker, so it should say that; cloche adds no answering command of its own.

The run is recorded like any other (run history, token usage). It does not start when the daily
budget gate is closed or the module has no `judge.md`. A judge that fails changes nothing: the
questions stay unanswered and the ask still exits 0.

A project replaces the built-in by defining a host workflow named `judge` (same mechanism as
`handle-notification`). It gets the ids as KV `question_ids` (one per line) and the module's
`judge.md` as `judge_guide`, read with `cloche get`.

### Conformance

`internal/module/qaconformance` is a shared suite every backend module should pass: ask,
open, answer, none-open, read, and human-beside-judge. Implement its `Backend` interface
(run the three slots, plus a way to answer through the module's own tool) and call
`qaconformance.Run(t, newBackend)`. Its embedded `stubmodule/` (scripts over a `qa-stub`
tool) is the reference implementation and the suite's own test.

## Asking in Saga: the `saga` qa backend

`.cloche/modules/saga` is a `qa` backend over Saga. Bind it to the `qa` slot and give it
the Saga project:

```
bind qa = saga { env.SAGA_PROJECT = "cloche" }
```

`cloche questions ask|await|read` then run the module's workflows. Design:
[`docs/plans/2026-11-13-qa-backends-design.md`](plans/2026-11-13-qa-backends-design.md).

| Workflow | `kv:` | `out:` | Notes |
|---|---|---|---|
| `ask` | `questions_file` | `question_ids` | One `saga ask -p $SAGA_PROJECT -f <feature> --file <file> --json` batch; the feature is the file's `backend.feature`. |
| `await-answer` | `question_ids` | `questions_open` | One non-blocking `saga show <id> --json` per id; open means no answer yet. |
| `read-answer` | `question_id` | `answer_path` | Writes the ruling in force to a file, a human's answer preferred over an agent's (the latest if none is human's). The file has a `Selected: <labels>` line for chosen options, then the text; either alone when only one is present. Fails when unanswered. |

Requires `saga` and `jq` on the host `PATH`. See the module README.

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
