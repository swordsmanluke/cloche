---
id: req-a531
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - unknown command error from cloche CLI
    - adding a new multi-word cloche subcommand
    - changing how cloche parses os.Args / dispatches commands
    - status --all not working
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 807b25b555dd358fa7a89c2dfe742aab7b95c68e
    extracted_at: 2026-09-16T15:24:08Z
    extracted_by: intent-scan
created: 2026-09-16T15:25:44.431829548Z
updated: 2026-09-16T15:25:44.431829548Z
---

CLI subcommands that dispatch on multi-word invocations (e.g. `status --all`, `loop status`, `project repos list`, `threads list --no-color`) need explicit parsing tests, because a change to the top-level dispatcher can silently break all of them at once by treating the whole argument string as a single command name.

**Why:** `cloche status --all` regressed to 'unknown command: status --all' after an unrelated CLI fixes ticket changed dispatch; the fix restored the flag and added tests, and the same regression risk applied to loop status, project repos list, and threads list --no-color.
