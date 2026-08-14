# 2. Create a Bead Task

Cloche's generated setup reads work from [beads](https://github.com/steveyegge/beads),
a git-native issue tracker driven by the `bd` CLI. Issues ("beads") live in
`.beads/` in your repo.

## The starter tasks

`cloche init --new` already created two:

```bash
bd ready
```

shows **task 1** — *Validate Agent works* — which asks the agent to create a
file containing `I exist!`. **Task 2** — *Clean up cloche test files* —
exists but isn't listed: it was created with a dependency on task 1
(`--deps`), so it stays blocked until task 1 is done. That dependency gate is
exactly how you'll sequence your own work.

## The five commands you'll actually use

```bash
bd create "Add a /health endpoint" -d "Return 200 and a version string"   # new task
bd ready                     # what's unblocked and waiting
bd show <id>                 # full detail for one task
bd update <id> -s open       # change status (open / in_progress / closed)
bd close <id>                # done
```

Useful extras: `-p 0`–`-p 4` sets priority at create time, and
`--deps <id>` makes the new task wait for another one.

## How the loop consumes them

The `list-tasks` workflow in `.cloche/host.cloche` runs
`.cloche/scripts/get-tasks.py`, which asks `bd ready --json` for open,
unblocked tasks and hands them to the daemon. When a task is dispatched, the
scripts claim it (`in_progress`), run the agent, and close it on success —
you never move statuses by hand unless something goes wrong.

Write task descriptions the way you'd brief a competent contractor: what to
build, where it lives, and how to tell it works. The description becomes the
agent's prompt (tutorial 4 shows the mechanism).

**Next:** [3. Run the loop](3-run-the-loop.md)
