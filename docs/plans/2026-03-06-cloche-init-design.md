# `cloche init` Update Design

**Date:** 2026-03-06
**Status:** Design

## Problem

`cloche init` is out of date. It does not create `config.toml`, `host.cloche`,
`scripts/`, or `version`. Its `.gitignore` strategy inverts the right default: it
ignores `.cloche/*/` then negates specific subdirectories, which means any new
directory added to `.cloche/` is accidentally ignored until explicitly whitelisted.

## New `.gitignore` strategy

Track everything in `.cloche/` by default. Explicitly ignore only runtime artifacts
(per-run output directories and transient counters).

```
# Cloche runtime artifacts
.cloche/*-*-*/
.cloche/run-*/
.cloche/attempt_count/
.gitworktrees/
```

**Pattern rationale:**

- `.cloche/*-*-*/` — matches all named run output dirs (`develop-bold-hawk/`,
  `orchestrate-bright-reef/`, etc.). Run IDs are always `<workflow>-<adj>-<noun>`,
  so any `.cloche/` subdirectory with two or more hyphens is a run artifact.
  Config directories (`prompts/`, `overrides/`, `scripts/`, `evolution/`) have no
  hyphens and are unaffected.
- `.cloche/run-*/` — legacy numeric run output dirs (`run-1772044065239145675/`).
- `.cloche/attempt_count/` — runtime step-attempt counters written by the agent.
- `.gitworktrees/` — existing entry, unchanged.

The old entries (`.cloche/*/`, negation rules) are removed when found.

## Conflict safety

Containers write only to `.cloche/<run-id>/output/` (their own run directory, which
is gitignored). Self-reflection and prompt curation run in the daemon process and write
to committed paths (`.cloche/prompts/`, `.cloche/version`). No container will modify
committed `.cloche/` files. No merge conflicts.

## Files created by `cloche init`

All files are skipped (with a message) if they already exist.

```
.cloche/
  config.toml          # project settings (new)
  version              # project version counter, content: "1" (new)
  <workflow>.cloche    # container workflow definition (existing)
  host.cloche          # host orchestration workflow (new)
  Dockerfile           # container image (existing)
  prompts/
    implement.md       # (existing)
    fix.md             # (existing)
    update-docs.md     # (existing)
  scripts/
    prepare-prompt.sh  # host step: generates prompt from task env vars (new)
  overrides/           # (existing, directory only)
```

### `config.toml` content

```toml
# Cloche project configuration

[orchestration]
enabled     = false
concurrency = 1
workflow    = "develop"
# tracker = "beads"

[evolution]
enabled            = true
debounce_seconds   = 30
min_confidence     = "medium"
max_prompt_bullets = 50
```

### `version` content

```
1
```

### `host.cloche` content

See host workflow design doc. The default replicates current hard-coded behavior:
prepare a prompt with a shell script, then dispatch the container workflow.

### `scripts/prepare-prompt.sh` content

```bash
#!/usr/bin/env bash
# Default prompt generator.
# Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT.
set -euo pipefail

prompt="## Task: ${CLOCHE_TASK_TITLE}

${CLOCHE_TASK_BODY}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
```

Created with mode `0755`.

## Next steps output

Updated `cloche init` output:

```
Initialized Cloche project in <name>

Next steps:
  1. Edit .cloche/config.toml    — enable orchestration, set concurrency
  2. Edit .cloche/<workflow>.cloche — adjust the test command for your project
  3. Edit .cloche/Dockerfile     — add your project's dependencies
  4. Edit .cloche/scripts/prepare-prompt.sh — customize prompt generation
  5. Add container-specific overrides to .cloche/overrides/ (e.g. CLAUDE.md)
  6. docker build -t cloche-agent -f .cloche/Dockerfile .
  7. cloche run --workflow <workflow> --prompt "..."
```

## Code changes

| File | Change |
|------|--------|
| `cmd/cloche/init.go` | Add `config.toml`, `version`, `host.cloche`, `scripts/prepare-prompt.sh` to file list; create `scripts/` dir; rewrite `addGitignoreEntries` call with new pattern set |
