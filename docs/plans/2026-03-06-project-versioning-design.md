# Project Versioning Design

**Date:** 2026-03-06
**Status:** Design

## Problem

After a self-reflection cycle mutates a workflow or prompt file, there is no record of
how many times the project has been evolved. This makes it hard to correlate behavior
changes with specific evolution events.

## Design

### Version file

A plain integer stored in `.cloche/version`. The file is created with value `1` on
`cloche init`. The daemon increments it whenever the evolution pipeline produces at
least one change.

```
.cloche/version
```

Content is a single decimal integer followed by a newline, e.g. `3\n`.

### Increment rule

In `internal/evolution/orchestrator.go`, after Stage 5 (Audit), check:

```go
if len(result.Changes) > 0 {
    incrementProjectVersion(o.cfg.ProjectDir)
}
```

`incrementProjectVersion` reads `.cloche/version`, parses the integer, increments it,
and writes it back atomically (write to a temp file, rename). If the file does not
exist, it writes `2` (treating the missing file as implicit version 1).

### Read path

`config.Load` gains a companion function:

```go
func LoadVersion(projectDir string) (int, error)
```

Returns the integer from `.cloche/version`, defaulting to `1` if the file does not
exist.

### Surface

- The project detail page (see project-pages design) reads the version via
  `/api/projects/{name}/info` and displays it as `v3`.
- The evolution audit log entry already records `ChangesJSON`; the version is not
  duplicated there.

### .gitignore

`.cloche/version` is **not** gitignored — it should be committed alongside workflow
changes so the version is visible in git history.

`cloche init` adds `.cloche/version` to the files it creates (with content `1\n`) but
does not add it to `.gitignore`.

## Changes required

| File | Change |
|------|--------|
| `internal/evolution/orchestrator.go` | Call `incrementProjectVersion` when `len(result.Changes) > 0` |
| `internal/config/config.go` | Add `LoadVersion(projectDir string) (int, error)` and `incrementProjectVersion` (unexported helper used by evolution) |
| `cmd/cloche/init.go` | Write `.cloche/version` with content `1\n` on init |
| `internal/adapters/web/handler.go` | Include version in `/api/projects/{name}/info` response |
