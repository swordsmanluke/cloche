---
id: req-0bf4
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - this ticket adds a new major feature, how do I version it
    - does this bead task need a minor version tag
    - backward-incompatible change ticket tagging policy
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: CLAUDE.md#versioning-policy-for-bead-tickets
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.453446144Z
updated: 2026-09-14T15:33:28.453446144Z
---

When creating a bead ticket for a change that needs a minor version bump (new major feature or backward-incompatible change), tag the ticket so the bump is handled during the finalize/merge workflow, rather than bumping ad hoc.

**Why:** Keeps version bumps centralized in one place (finalize) instead of scattered across individual task commits.
