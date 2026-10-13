# Modules: Shareable, Swappable Workflow Packages

**Date:** 2026-10-13
**Status:** Proposed. Supersedes [2026-09-14-reusable-modules-design.md](2026-09-14-reusable-modules-design.md).
**Decision record:** Socratic feature `cloche/modules`, batches CLOCHE-B4 to CLOCHE-B7.
Answer ids (`CLOCHE-Ann`) are cited inline.

## Problem

A cloche project's behavior lives in loose `.cloche` files, `scripts/` and `prompts/`.
Every project re-implements the same flows, such as task tracking, code review and
development loops. There are two consequences:

- A flow can't be **shared**. Adopting someone else's Jira integration means copying
  scripts and renaming files by hand.
- A flow can't be **swapped**. Moving from Beads to Jira, or comparing a TDD develop loop
  with a "yolo" one, means rewriting the caller.

Data passed between workflows is also implicit. A script reads `task_id` from the KV
store, but nothing declares that it needs to.

## Goals

- **Shareable.** A module is a directory of `.cloche` files, scripts and prompts that
  is fetched from git or a local path, pinned, and vendored into the project.
- **Swappable.** A workflow call names a *slot* (`code-review`). A `bind` decides which
  workflow fills that slot. Swapping an implementation changes one line in a `.cloche`
  file (CLOCHE-A32).
- **Explicit.** Every workflow can declare a `contract`: the KV keys and env vars it
  reads and the KV keys it writes. Cloche checks contracts statically (`cloche validate`)
  and at runtime (CLOCHE-A42).

## Non-goals (v1)

- Named, published or versioned contracts (CLOCHE-A19, CLOCHE-A30, CLOCHE-A32).
  Compatibility is structural. Standard contracts are documented shapes, not registry
  objects.
- Installing a module's container prerequisites. A module documents them in its README
  (CLOCHE-A27).
- Override or overlay layers for customizing a module. A vendored module is plain files
  in your repo, so you edit them directly (CLOCHE-A28).
- Result sets other than `success` and `fail` (CLOCHE-A34).
- A per-run CLI override such as `cloche run --bind` (CLOCHE-A46).
- Module settings in `config.toml`. Module settings arrive as contract inputs
  (CLOCHE-A27, CLOCHE-A41).

## Concepts

| Concept | Meaning |
|---|---|
| **Module** | A directory with `module.toml`, `README.md` and any number of `.cloche` files, `scripts/` and `prompts/`. |
| **Contract** | A workflow's declared signature: inputs (`kv:` and `env:`), outputs (`out:`), and results (always `success` and `fail` in v1). It is declared on the workflow itself (CLOCHE-A20). |
| **Slot** | The workflow name used at a call site (`workflow = code-review`). |
| **Bind** | A mapping from a slot to a concrete workflow, scoped to a step, a workflow, a file or the project. |
| **Compatible** | A workflow fits a call site if every input it declares is available at that point and it writes every key the downstream steps read (CLOCHE-A19, CLOCHE-A35). |

## Module layout

```
.cloche/modules/jira-tasks/
├── module.toml
├── README.md
├── tasks.cloche          # exports list-tasks, claim-task, close-task, release-task
├── scripts/
│   ├── list.sh
│   └── claim.sh
└── prompts/
```

### `module.toml`

```toml
name        = "jira-tasks"
description = "Task source backed by Jira issues"

# Workflows callable from outside the module. Any other workflow is private (CLOCHE-A39).
exports = ["list-tasks", "claim-task", "close-task", "release-task"]

# Host binaries the module's host-side scripts need. `cloche doctor` checks them (CLOCHE-A27).
requires = ["curl", "jq"]

# Secrets the module expects to find in the credentials mechanism (CLOCHE-A27).
credentials = ["JIRA_TOKEN"]
```

Configuration values such as `jira_url` are not declared here. They are ordinary
contract inputs, supplied through the KV or the environment and remapped with `with`
when needed (CLOCHE-A41).

### `README.md` (required)

The README has standard sections: **Provides** (the exported workflows and their
contracts), **Inputs and configuration**, **Prerequisites** (host binaries and the
container image's needs), and **Credentials**. `cloche module add` prints the
Prerequisites section. Installing anything into the container image is left to the
user (CLOCHE-A27).

### Paths inside a module

Inside a module's `.cloche` files, `file("prompts/x.md")` resolves against the module
root. Every `run` command gets `CLOCHE_MODULE_DIR`, which is the module's host path on
the host and its `/workspace/...` path in a container. Scripts therefore call
`"$CLOCHE_MODULE_DIR/scripts/claim.sh"` (CLOCHE-A44).

Module workflows run exactly like project workflows. A container workflow's scripts
run in the container and a host workflow's scripts run on the host. Modules are not
restricted to the host (CLOCHE-A31).

Vendored modules live under `.cloche/`, so they are copied into containers along with
the rest of the project root. A local-path module outside the project is copied in at
`/workspace/.cloche/modules/<name>/`, the same way `overrides/` is applied.

## Contracts

A workflow declares its contract with a `contract { }` block (CLOCHE-A33):

```
workflow code-review {
  contract {
    kv:  child_branch       # reads KV key child_branch
    kv:  task_id
    env: REVIEW_MODEL       # reads env var REVIEW_MODEL
    out: review_path        # writes KV key review_path
  }

  step review { ... }
  ...
}
```

- **`kv:`** is a required KV key. **`env:`** is a required env var. **`out:`** is a KV
  key the workflow guarantees to write when it ends in `done`. Env vars can't be
  outputs, so a workflow returns data only through KV keys and its result.
- **Results** are `success` (the workflow reached `done`) and `fail` (it reached
  `abort`). Results are part of the contract, and in v1 that set is fixed
  (CLOCHE-A22, CLOCHE-A34).
- The `contract` block is optional. A workflow without one makes no static claims and
  is checked only on its results. `cloche validate` warns when an **exported** module
  workflow has no contract.
- Contracts are not versioned (CLOCHE-A30).

### Runtime enforcement (CLOCHE-A42)

- **Before the child run starts:** every `kv:` key must resolve in the child's KV after
  `with` remapping, and every `env:` var must be set. A missing input fails the
  calling step with `contract: code-review requires kv:child_branch (not set)` and the
  child never starts.
- **When the child ends in `done`:** every `out:` key must be set, or the step fails
  with a contract error. Writing undeclared keys is allowed.

## Call sites and `with`

`workflow = <slot>` is the existing `workflow_name` step. It gains an optional `with`
block that remaps inputs and outputs (CLOCHE-A21, CLOCHE-A43, CLOCHE-A50, CLOCHE-A53):

```
step review {
  workflow = code-review
  with {
    task_id          from ticket_id        # input: child's kv task_id <- caller's kv ticket_id
    env.REVIEW_MODEL = "opus"              # input: literal into an env var
    env.JIRA_URL     from jira_url         # input: env var filled from a KV key
    feedback_path    from out.review_path  # output: caller's kv feedback_path <- child's review_path
  }
  results = [success, fail]
}
```

- A bare name is a KV key and `env.NAME` is an env var (CLOCHE-A53). Template syntax
  is unchanged. A wider syntax cleanup is deferred to v4.
- `out.<key>` on the right-hand side refers to a key the child writes, which is how an
  output is remapped back into the caller's namespace.
- A `with` block applies only to the step it appears in. Remapped keys appear under
  their new names, and the original keys are still set too.

### Static compatibility check (CLOCHE-A35)

For each `workflow =` step, `cloche validate` resolves the slot through the binds in
scope, then checks:

1. **Inputs.** Each `kv:` input of the bound workflow must be available at that point.
   It can be seeded by the run (`task_id`, `run_id`, …), declared as an `out:` by an
   upstream workflow step, supplied by `with`, or come from the caller's own contract
   `kv:` inputs. A key written by an untyped script step can't be seen statically, so
   such an input produces a **warning** and the runtime check backs it up.
2. **Outputs.** Every key that a later `workflow =` step's contract reads, and that
   nothing else in this workflow provides, must be an `out:` of the bound workflow.
3. **Results.** The step's wiring must cover `success` and `fail`.

## Binds

`bind` maps a slot to a workflow. Where a bind is written decides its scope
(CLOCHE-A47):

```
# Several binds at one level
bind {
  code-review = ruby-review
  develop     = tdd-develop
}

# One bind
bind code-review = ruby-review
```

### Right-hand side (CLOCHE-A38)

| Form | Resolves to |
|---|---|
| `ruby-review` | If a module named `ruby-review` is installed, its exported workflow named after the slot. Otherwise the project workflow `ruby-review`. A name that matches both is a validation error. |
| `ruby-review:review` | Exported workflow `review` of module `ruby-review`. |
| `quick-review` | Project workflow `quick-review`, when no module has that name. |

Module workflows are reachable **only** through a bind. An unbound slot resolves to a
project workflow of that name, then to a built-in (CLOCHE-A39).

### Scopes (CLOCHE-A37, CLOCHE-A47, CLOCHE-A51)

| Position | Scope |
|---|---|
| Top level of `.cloche/project.cloche` | **Project root.** Applies to daemon-launched phases and every run. |
| Top level of any other `.cloche` file | That file's workflows and everything they call. |
| Inside a `workflow { }` body | That workflow and everything it calls. |
| Inside a `step { }` body | That step's call only, including what the child calls. |

Only `project.cloche` can hold project-root binds. A project with no `project.cloche`
has an empty root scope.

### Resolution (CLOCHE-A36, CLOCHE-A52)

Binds are **dynamic**: a bind reaches into every sub-workflow called beneath its
scope, including module workflows. To resolve a slot, cloche walks from the call site
outward:

```
step → enclosing workflow → that workflow's file
  → (caller's step → caller's workflow → caller's file) …
  → project.cloche root → project workflow of that name → built-in
```

The **innermost** bind wins. A module that binds a slot internally gets its own
choice, because the module knows what it needs.

**Forced override.** `bind!` takes precedence over every bind beneath it, including a
module's own binds:

```
# security.cloche
bind! lint = standardrb-lint   # wins even over ruby-review's internal `bind lint = rubocop-lint`
```

When two `bind!` binds compete for the same slot on a call path, the outer one wins.

### Example

```
# .cloche/project.cloche
bind {
  list-tasks   = jira-tasks
  claim-task   = jira-tasks
  close-task   = jira-tasks
  release-task = jira-tasks
  code-review  = ruby-review
}

# .cloche/security.cloche
bind code-review = sec-review     # every code-review under security-path uses sec-review

workflow security-path {
  host {}
  step review { workflow = code-review  results = [success, fail] }
  step quick  {
    workflow = code-review
    bind code-review = quick-review  # this call only
    results = [success, fail]
  }
  ...
}
```

Swapping Jira back to Beads means changing four right-hand sides in `project.cloche`.

## Standard contract: task management (CLOCHE-A24, CLOCHE-A25, CLOCHE-A45)

The daemon's orchestration loop is the first standard contract. A task tracker module
implements the four tracker workflows. `main` remains the project's own workflow.

| Workflow | `kv:` inputs | `out:` outputs | Notes |
|---|---|---|---|
| `list-tasks` | — | `tasks_path` | The file holds JSONL in the current task format. |
| `claim-task` | `task_id` | `task_title`, `task_body_path` | **New phase.** The daemon runs it before `main`. A lost claim ends in `fail` with `claim_status = unavailable`. The daemon treats that as "skip for the dedup window" rather than as an error (CLOCHE-A49). |
| `main` | `task_id`, `task_title`, `task_body_path` | — | Project-owned. It no longer claims the task itself. |
| `close-task` | `task_id` | — | Also invoked by the dashboard's "Close in tracker" action. |
| `release-task` | `task_id` | — | Also invoked by the dashboard's release action. |

The daemon resolves each phase through the project root scope.

**Migration.** Today `list-tasks` prints JSONL on its final step's stdout, and `main`
claims the task in its first step. During the transition:

- If `tasks_path` is unset, the daemon falls back to parsing stdout.
- If no `claim-task` resolves, the daemon skips that phase and `main` keeps claiming
  for itself.

A future minor version removes both fallbacks.

**First proof.** Move this repo's Beads scripts (`ready-tasks.sh`, `claim-task.sh`,
`close-task.sh`, `release-task.sh`) into a `beads-tasks` module. Write a second
tracker, Jira or GitHub Issues, against the same contract. Swap between them in
`project.cloche` with no other edits.

## Distribution (CLOCHE-A26, CLOCHE-A31)

```
cloche module add https://github.com/acme/cloche-jira@v1.2.0   # git source, ref pinned
cloche module add ../shared/my-review                           # local path
cloche module list
cloche module update jira-tasks       # shows the diff of scripts and prompts, asks before applying
cloche module remove jira-tasks
```

- A **git source** is resolved to a commit and recorded in `.cloche/modules.lock`
  (name, source, ref, commit, content hash). Its files are vendored into
  `.cloche/modules/<name>/` and committed. Builds work offline, and the module's code
  can be reviewed in diffs.
- A **local path** module is recorded in the lockfile with `source = "path:..."` and
  is used in place, not vendored. Edits show up immediately.
- Nothing in a module runs at install or update time. `update` shows the full diff and
  waits for confirmation (the "pin and review" choice in CLOCHE-A31).
- `cloche validate` warns when a vendored module's content hash no longer matches the
  lockfile. That is expected after local edits (CLOCHE-A28) and serves as a reminder
  that `update` will overwrite them.

### Dependencies (CLOCHE-A29)

A module can call slots it doesn't provide, for example `workflow = claim-task`. Those
slots resolve through the binds of whoever called the module. No transitive fetch
takes place. `module.toml` may list `uses = ["claim-task"]` so that `cloche validate`
can report an unbound slot clearly.

## Validation summary

`cloche validate` adds these checks:

- Each `module.toml` parses. Each name in `exports` exists as a workflow in the module.
  The module has a README.
- Workflow names are unique within a module. They can't collide with the project
  because module workflows are namespaced (CLOCHE-A39).
- Bind targets exist, and a bare right-hand side is not ambiguous between a module and
  a project workflow.
- Every `workflow =` step resolves to something, using its full static bind chain. For
  dynamic binds, validation checks each call path from the root and from each
  workflow's own file.
- The contract compatibility checks described above.
- `requires` binaries are present on the host. `cloche doctor` runs this check too.
- `credentials` names are present in the credentials store, reported as a warning.

## Implementation sketch

1. **DSL.** Parse `contract { kv: env: out: }`, `with { }`, `bind`, `bind { }` and
   `bind!` at file, workflow and step level. Attach binds to `domain.Workflow` and
   `domain.Step`, and record each workflow's source file and module.
2. **Discovery.** `FindAllWorkflows` also loads `.cloche/modules/*/` and local-path
   modules from the lockfile into a namespaced registry. It loads
   `.cloche/project.cloche` as the root scope.
3. **Resolver.** `ResolveSlot(callStack, slot)` walks the scope chain and handles
   `bind!`. Both host and container dispatch call it. The call stack, as a list of
   scope frames, is passed to child runs so dynamic scoping crosses run boundaries.
4. **Runtime.** Apply `with` to the child's KV and env when seeding it. Check inputs
   before dispatch and outputs on `done`. Set `CLOCHE_MODULE_DIR`. Copy external
   local-path modules into containers.
5. **Orchestration.** Add the `claim-task` phase, `tasks_path` and `claim_status`
   handling, with the fallbacks described above.
6. **CLI.** `cloche module add|list|update|remove`, the lockfile and vendoring.
7. **Proof.** Extract a `beads-tasks` module from this repo, write a second tracker,
   and document both in `docs/modules.md`.

Versioning: this adds new major features, so it needs a **minor** bump (CLAUDE.md
versioning policy).

## Open questions

- **Static checking of dynamic binds.** Every call path is enumerable, so the check is
  feasible. The cost is high in projects with deep nesting. Checking only paths
  reachable from daemon phases and `cloche run` entry points may be enough.
- **Dashboard and `cloche status`.** These should show which bind supplied each
  `workflow =` step, for example `code-review → sec-review (security.cloche)`, so
  comparison runs are legible.
- **Exporting to non-cloche consumers.** A module could also ship as a plain git
  subtree. That is out of scope for now.
