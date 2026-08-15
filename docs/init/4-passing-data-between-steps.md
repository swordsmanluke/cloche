# 4. Passing Data Between Steps

Steps are separate processes — some on the host, some inside a container —
so data between them is passed explicitly through the run's **key-value
store**, plus files for anything big. Three tools:

| Where | Write | Read |
|-------|-------|------|
| Host script | `cloche set <key> <value>` | `cloche get <key>` |
| Container step | `clo set <key> <value>` | `clo get <key>` |
| Agent prompt | — | `{{ $key }}` template variable |

The store is scoped to the run (host and container sides see the same keys)
and wiped when the attempt ends. **Values are capped at 1 KB** — anything
bigger travels as a *file path*, never as a value.

## The worked example: how the agent gets its task

The generated scaffold passes the task description from beads to the agent in
the container. Follow the data:

1. **`prepare-prompt.py`** (host) reads the task from `bd show`, writes it to
   a file, and publishes the *path*:

   ```python
   temp_dir = cloche get temp_file_dir        # .cloche/runs/<run-id>, daemon-created
   write(temp_dir + "/task_prompt.md", prompt)
   cloche set task_prompt_path <that path>
   ```

2. **`implement.md`** (agent prompt, in the container) reads it back:

   ```
   cat /workspace/$(clo get task_prompt_path)
   ```

   This works because `temp_file_dir` is project-relative and the run
   directory is mounted into the container under `/workspace`.

Copy this shape whenever one step produces something another step needs.

## Prompt template variables

Agent-step prompts (and only prompts — `run` commands get plain shell) are
templated before the agent sees them:

| Syntax | Meaning |
|--------|---------|
| `{{ $name }}` | Value of a builtin or KV key |
| `{{! cmd }}` | Run `cmd` in a shell, substitute its stdout |
| `{{@ path }}` | Insert a file's contents |

Builtins: `task_id`, `run_id`, `step_name`, `workdir`, `prev_output` (the
previous step's captured output), `task_description`. Anything else is looked
up in the KV store. An unresolvable directive **fails the step** before the
agent runs — a misspelled key can't silently produce an empty prompt.

The generated `fix-tests.md` uses `{{ $prev_output }}` to show the agent the
failing test output; `fix-merge.md` uses `{{ $worktree_path }}` and
`{{ $base_branch }}`, which `merge.py` published with `cloche set` just
before failing. That's the KV → prompt pattern in one line: a script `set`s a
key, a later prompt references `{{ $key }}`.

> Older templates used single-brace `{task_description}` placeholders. They
> still work but are deprecated and warn on every run — use `{{ $... }}`.

## Keys the daemon sets for you

Every run starts with `temp_file_dir` already set (and the directory
created). After a container sub-workflow finishes, the daemon also publishes
`child_branch`, `child_run_id`, and friends — tutorial 5 uses those. The full
table is in [workflows.md](https://github.com/swordsmanluke/cloche/blob/main/docs/workflows.md#built-in-kv-keys).

**Next:** [5. How changes land](5-how-changes-land.md)
