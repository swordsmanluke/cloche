# Getting Started with Cloche

Six short tutorials that walk you from an empty project to an autonomous
task loop. Each covers one step; read them in order the first time.

1. [Initialize a project](1-initialize-a-project.md) — `cloche init --new` and what it generates
2. [Create a bead task](2-create-a-bead-task.md) — the default task tracker in five minutes
3. [Run the loop](3-run-the-loop.md) — start, watch, and stop the orchestration loop
4. [Passing data between steps](4-passing-data-between-steps.md) — the KV store, files, and prompt templates
5. [How changes land](5-how-changes-land.md) — worktrees, branches, and the merge step
6. [Swapping the task tracker](6-swapping-the-task-tracker.md) — replace beads with your own tracker

## Prerequisites

- **Docker** — agents run in containers; the daemon builds and manages them.
- **A coding agent CLI** (e.g. `claude`) — authenticated on the host, used
  inside the container and for init's placeholder-filling phase.
- **beads (`bd`)** — the default task tracker
  ([github.com/steveyegge/beads](https://github.com/steveyegge/beads)).
  Optional if you bring your own tracker (tutorial 6), but the generated
  setup assumes it.
- **`cloched` running** — the Cloche daemon. `cloche doctor` verifies all of
  the above.

## The shape of the system

```
cloche run/loop  ──gRPC──▶  cloched (daemon, host)  ──▶  Docker container
                             │  runs host.cloche          │  runs develop.cloche
                             │  steps on the host         │  steps via cloche-agent
                             └── key-value store ◀──clo get/set──┘
```

Everything the tutorials touch lives in `.cloche/` at your project root;
`cloche init --new` generates all of it.
