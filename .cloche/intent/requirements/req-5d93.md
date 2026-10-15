---
id: req-5d93
status: active
scope:
    level: domain
    domains:
        - cli
hints:
    - added a new cloche subcommand
    - tab completion missing command
    - where is the subcommand list
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: dae21f8e6db8823fbae4d1ec4ec88a20e322032d
    extracted_at: 2026-10-15T16:16:50.445762526Z
    extracted_by: intent-scan
created: 2026-10-15T16:16:50.494861843Z
updated: 2026-10-15T16:16:50.494861843Z
---

New CLI subcommands must be added in every place the subcommand list lives: main dispatch, help text, shell completion (`completionSubcommands`), docs/USAGE.md and the subcommand list in CLAUDE.md.

**Why:** The `module` command change touched all of these together; the lists are maintained by hand and drift if one is missed.
