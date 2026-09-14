# Seed task list

`seed-tasks.json` is a `bd create --graph` plan (see `bd create --help`)
defining the 12 dependency-ordered Bract build tasks: lexer, parser, evaluator
core, bindings, functions/closures, control flow, errors, REPL, file runner,
formatter, polish, docs. Each node is blocked on the previous one (a strict
chain), so `bd ready` surfaces exactly one task at a time in that order.

Drift corrections D1–D5 (see
`docs/plans/2026-09-14-intent-ab-experiment-protocol.md` in the cloche repo)
are embedded verbatim in the descriptions of `04-bindings`, `05-functions`,
`06-control-flow`, `08-repl`, and `09-file-runner`, phrased as offhand
corrections rather than called out specially — that's intentional, not a
formatting inconsistency.

This file is not imported automatically. `.beads/` is git-ignored (task
tracker state is local, reproducible from this file), so each arm clone
bootstraps its own tracker before starting the loop:

```bash
bd init --non-interactive
bd create --graph .cloche/tasks/seed-tasks.json
```

Verify the import before starting a loop: `bd ready --json` should return
only `01-lexer` until it's closed, then `02-parser`, and so on — this is
what `.cloche/scripts/get-tasks.py` (the `list-tasks` workflow) relies on.
