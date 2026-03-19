# `cloche init` v2 Scaffold Update Design

**Date:** 2026-03-19
**Status:** Design

## Problem

`cloche init` scaffolds files that reflect the v1 project structure. The v2
architecture introduced new runtime directories (`.cloche/logs/`, `.cloche/runs/`),
a task-oriented orchestration loop, and config fields (`[orchestration]`) that the
scaffold doesn't expose. The demo script (`prepare-prompt.sh`) is Bash, which is
less portable and less readable than Python. The post-init guidance still tells
users to invoke `cloche run --workflow`, but the intended v2 workflow is
`cloche loop`.

## Solution

Update every template string in `cmd/cloche/init.go` and the `.gitignore` logic
to match v2 conventions. Replace the Bash demo script with Python. Update
post-init guidance to steer users toward `cloche loop`.

## Design Details

### .gitignore

Replace the current `addGitignoreEntries` / `removeGitignoreEntries` calls with
a single `addGitignoreEntries` call for the v2 patterns:

```
.cloche/logs/
.cloche/runs/
.gitworktrees/
```

Remove the `removeGitignoreEntries` call entirely — new projects have no legacy
patterns to clean up.

### .clocheignore

Update `defaultClocheignore` to exclude the v2 runtime directories instead of
the old run-id patterns:

```
# Files excluded from the container workspace.
# Uses gitignore-style patterns (*, ?, **).

# Cloche runtime state
.cloche/logs/
.cloche/runs/

# Common large/generated directories
node_modules/
.venv/
venv/
__pycache__/
dist/
build/
.next/
target/

# IDE / editor
.idea/
.vscode/
*.swp
*.swo
```

### config.toml

Add a commented-out `[orchestration]` section so users can discover the
available knobs:

```toml
# Cloche project configuration
# Set active = true so cloched picks up tasks automatically.
active = false

# [orchestration]
# concurrency            = 1
# stagger_seconds        = 1.0
# dedup_seconds          = 300.0
# stop_on_error          = false
# max_consecutive_failures = 3

[evolution]
enabled            = true
debounce_seconds   = 30
min_confidence     = "medium"
max_prompt_bullets = 50
```

### host.cloche

Change the `prepare-prompt` step to invoke Python:

```
workflow "main" {
  step prepare-prompt {
    run     = "python3 .cloche/scripts/prepare-prompt.py"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}
```

### scripts/prepare-prompt.py (replaces prepare-prompt.sh)

The file list entry changes from `prepare-prompt.sh` to `prepare-prompt.py`.
The script remains 0755.

```python
#!/usr/bin/env python3
"""Default prompt generator.

Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT (if set).
"""
import os
import sys

title = os.environ.get("CLOCHE_TASK_TITLE", "")
body = os.environ.get("CLOCHE_TASK_BODY", "")

prompt = f"## Task: {title}\n\n{body}"

print(prompt)

output_path = os.environ.get("CLOCHE_STEP_OUTPUT")
if output_path:
    with open(output_path, "w") as f:
        f.write(prompt + "\n")
```

### Post-init guidance

Replace the seven "Next steps" lines with:

```
Next steps:
  1. Edit .cloche/config.toml        — set active = true
  2. Edit .cloche/{workflow}.cloche   — adjust the test command for your project
  3. Edit .cloche/Dockerfile          — add your project's dependencies
  4. Edit .cloche/scripts/prepare-prompt.py — customize prompt generation
  5. Add container-specific overrides to .cloche/overrides/ (e.g. CLAUDE.md)
  6. docker build -t cloche-agent -f .cloche/Dockerfile .
  7. cloche loop                      — start the orchestration loop
```

### Shell completion

No changes — `generateCompletionScripts` and `offerShellIntegration` remain as-is.

### version file

No change — stays at `1`. This tracks prompt/script evolution, not project
schema.

### Files unchanged

- `workflowTemplate` (container workflow) — no v2-driven changes needed.
- `dockerfileTemplate` — unchanged.
- `implementPrompt`, `fixPrompt`, `updateDocsPrompt` — unchanged.
- `versionContent` — stays `"1\n"`.

### Error Handling

No new error paths. The existing skip-if-exists and write-error behavior is
unchanged.

## Alternatives Considered

**Keep Bash for the demo script.** Rejected — Python is more readable for
multi-line string construction and is universally available on the target
platforms (Linux, macOS). Bash quoting around `$CLOCHE_TASK_BODY` is fragile
when the body contains special characters.

**Add `.cloche/logs/` and `.cloche/runs/` directories during init.** Rejected —
these are created on demand by the daemon. Scaffolding empty directories adds
noise and requires placeholder files.

**Bump the version file to 2.** Rejected — the version file tracks prompt/script
evolution iterations, not the project schema version. A fresh project starts at
iteration 1.
