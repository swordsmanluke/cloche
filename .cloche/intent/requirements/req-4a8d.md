---
id: req-4a8d
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - can we rename a config key without a deprecation alias
    - breaking change to a config key that hasn't shipped in a release yet
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-09-14-intent-builtin-migration-design.md
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.462674736Z
updated: 2026-09-15T00:41:34.462674736Z
---

Renaming or removing a config key for a feature that hasn't yet shipped in a release needs no deprecation alias — the "don't break existing config" convention only kicks in once a key has actually shipped.

**Why:** Used explicitly to justify renaming intent = "off" (string) to intent_tracking = false (bool) with no back-compat shim, since the feature hadn't shipped yet.
