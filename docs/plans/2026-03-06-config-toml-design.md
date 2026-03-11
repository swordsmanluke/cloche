# Project Settings File: config.toml

**Date:** 2026-03-06
**Status:** Design

## Problem

The per-project settings file is `.cloche/config` with no extension, making it
ambiguous and inconsistent with the TOML library in use.

## Change

Rename to `.cloche/config.toml`. No backwards compatibility with the old filename.

## Default file (created by `cloche init`)

```toml
# Cloche project configuration

[orchestration]
concurrency        = 1
stagger_seconds    = 1.0
# list_tasks_command = "bash .cloche/scripts/ready-tasks.sh"
# dedup_seconds    = 300

[evolution]
enabled           = true
debounce_seconds  = 30
min_confidence    = "medium"
max_prompt_bullets = 50
```

Notes:
- `orchestration.concurrency` defaults to `1` (single run at a time).
- `orchestration.stagger_seconds` adds delay between consecutive launches (default `1.0`).
- `orchestration.list_tasks_command` (optional) is a shell command that outputs a JSON array of
  task objects (`[{"id":"...","title":"...","description":"..."}]`). When set, the daemon
  picks tasks and passes the ID via `CLOCHE_TASK_ID` env var.
- `orchestration.dedup_seconds` (default `300`) prevents reassignment of the same task ID
  within the timeout window.
- Commented-out keys show optional overrides that rarely need changing.

## Code changes

| File | Change |
|------|--------|
| `internal/config/config.go` | `Load()`: change path from `"config"` to `"config.toml"` |
| `cmd/cloche/init.go` | Add `config.toml` to the files created by init |
| `.cloche/config` (existing file in this repo) | Rename to `.cloche/config.toml` |
