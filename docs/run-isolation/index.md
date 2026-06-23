# Run Isolation & Safety

This section documents the cluster of features that let cloche run vertical tasks
**safely and concurrently** without one run corrupting another or reverting the
base branch — plus the operational guardrails that bound a run's cost and let an
operator quiesce the daemon for maintenance.

These features landed across cloche **v3.16 – v3.18.2** (and companion changes in
the wrapper's `.cloche/` vertical-workflow scripts).

## Why this exists

The vertical workflow runs many tasks, often concurrently, each building a stack of
git branches and finally merging it into `main`. Earlier, a run was seeded from — and
its host steps mutated — the **shared `repos/cloche` working tree**, and the daemon
would **auto-resume** interrupted runs by reusing their old container filesystem. The
result was a class of corruption where a stale or mid-rebase tree got copied into a
container, committed, and finalized back over `main` — silently reverting unrelated
work. Run isolation removes that whole class of failure.

The core idea: **every run takes a clean snapshot of the base on the way in, does all
of its branch surgery in throwaway worktrees, and only advances `main` with a
fast-forward refspec push — never by checking out or mutating the shared tree.**

```
            ┌── clean snapshot in ──┐                 ┌── isolated finalize out ──┐
 main ──────┤ git archive <baseSHA> │── container ────┤ rebase onto latest main   │──► main
 (shared    │ → temp → /workspace   │   (per run)     │ in a throwaway worktree,  │   (only ever
  tree      └───────────────────────┘                 │ push HEAD:refs/heads/main │   fast-forwarded)
  untouched)                                           └───────────────────────────┘
```

## Documents

- **[architecture.md](architecture.md)** — the per-run lifecycle, every isolation
  point, the finalize sync-forward protocol, resume rebuild, the operational
  guardrails, and how it all fits together. Includes the diagrams below.

## Diagrams

- **[run-lifecycle.svg](run-lifecycle.svg)** ([source](run-lifecycle.d2)) — how a run
  is isolated from the shared checkout and from `main`, end to end.
- **[finalize-flow.svg](finalize-flow.svg)** ([source](finalize-flow.d2)) — the
  sync-forward-before-merge-back finalize, including the conflict and push-rejected
  paths.

## Features at a glance

| Feature | Version | What it does |
|---|---|---|
| Clean per-run container snapshot | v3.18.0 | Seeds `/workspace` from `git archive <baseSHA>`, never the live tree. |
| Isolated finalize + sync-forward | wrapper | Merges the stack via a throwaway worktree, rebasing onto the latest base; pushes via refspec. |
| Close-on-publish | wrapper | Closes a layer's tracker task the moment its branch is published, so re-runs don't redo it. |
| `--allow-no-op` idempotency | wrapper | Re-dispatched test-plan/docs phases pass when their work is already in the base. |
| Resume rebuild + preserve workspace | v3.17.0 | `cloche resume` rebuilds the container fresh and re-applies a per-step workspace snapshot. |
| `token-limit` | v3.16 | Per-step and per-workflow **output**-token ceilings, enforced by the engine. |
| `cloche loop stop --hard` | v3.18.1 | Stops the loop **and** parks resumable runs so a restart won't auto-resume them. |
| godog self-registration | v3.18.2 | Each BDD feature self-registers, so two test plans never conflict in `TestMain`. |

## Related docs

- [Vertical workflow design](../design/vertical-workflow.md) — the three-phase
  pipeline these mechanisms protect.
- [Workflow DSL](../workflows.md) — `token-limit` config reference.
- [Usage](../USAGE.md) — step config keys, including `token-limit`.
- [Safety](../SAFETY.md) — the container/credential security model (orthogonal to
  the git-correctness isolation documented here).
