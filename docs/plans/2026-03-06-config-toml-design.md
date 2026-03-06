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
enabled     = false
concurrency = 1
workflow    = "develop"
# tracker = "beads"

[evolution]
enabled           = true
debounce_seconds  = 30
min_confidence    = "medium"
max_prompt_bullets = 50
```

Notes:
- `orchestration.enabled` defaults to `false` so new projects don't start pulling tasks
  until explicitly opted in.
- All keys are present so the file is self-documenting.
- Commented-out keys (`tracker`) show optional overrides that rarely need changing.

## Code changes

| File | Change |
|------|--------|
| `internal/config/config.go` | `Load()`: change path from `"config"` to `"config.toml"` |
| `cmd/cloche/init.go` | Add `config.toml` to the files created by init |
| `.cloche/config` (existing file in this repo) | Rename to `.cloche/config.toml` |
