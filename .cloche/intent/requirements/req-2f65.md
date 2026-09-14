---
id: req-2f65
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - is this a major version bump
    - should I bump minor or the build number
    - is this a breaking change that needs a version bump
    - what version number do I use for this change
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: CLAUDE.md#versioning
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.453221219Z
updated: 2026-09-14T15:33:28.453221219Z
---

Never bump the major version unless explicitly told to. Minor bumps are reserved for new major features and backward-incompatible changes; everything else bumps the build number.

**Why:** Major releases are batched manually at the maintainer's direction; automatic major bumps would take that decision away from them.
