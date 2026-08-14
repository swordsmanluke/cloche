# Design: Pluggable Task Tracking Systems

## Original Feedback

> It'd be nice to have a way to have pluggable connections to task
> tracking systems. With the right abstraction, a plugin for ado or
> jira or whatever could be dropped in and new users could be off to
> the races pretty quickly.

## Status: realized as a script contract

The abstraction landed as the `list-tasks` JSONL contract rather than a
plugin system: the daemon only ever consumes ready tasks as
`{"id","title","description","status"}` lines from the `list-tasks` host
workflow, and all tracker-specific behavior lives in five replaceable
scaffold scripts (get-tasks / claim-task / prepare-prompt / close-task /
unclaim). The default backend is beads (`bd`), bootstrapped by
`cloche init --new`.

The user-facing guide for swapping in ADO, Jira, GitHub Issues, etc. is
[docs/init/6-swapping-the-task-tracker.md](../init/6-swapping-the-task-tracker.md).
A richer typed-plugin abstraction (see `docs/design/primitives.md`) remains
open if the script contract proves limiting.
