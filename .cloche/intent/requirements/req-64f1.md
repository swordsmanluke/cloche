---
id: req-64f1
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - writing a new built-in workflow script
    - script step fails only on some machines
    - 'dash: set: Illegal option -o pipefail'
    - adding a shell script to a built-in workflow
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/CHANGELOG-DETAILED.md#v3.22.0
    extracted_at: 2026-09-15T18:11:18Z
    extracted_by: intent-scan
created: 2026-09-15T18:12:51.429079594Z
updated: 2026-09-15T18:12:51.429079594Z
---

Scripts used by built-in workflows (e.g. intent-scan) must be POSIX-sh compatible and avoid bashisms like `set -o pipefail`, since they can run under a system's default `/bin/sh` (e.g. Ubuntu's dash) rather than bash.

**Why:** A `set -o pipefail` bashism previously broke the intent-scan built-in scripts specifically under Ubuntu's default dash shell (fixed in commit 6200c30).
