---
id: req-7aa2
status: active
scope:
    level: domain
    domains:
        - versioning-release
hints:
    - changelog draft was wrong, what happens if I re-run cloche run changelog
    - will re-running changelog create a duplicate entry
    - fixing a bad changelog draft before tagging
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: CLAUDE.md#cutting-a-release
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.453675918Z
updated: 2026-09-14T15:33:28.453675918Z
---

Re-running the `changelog` workflow for the same version is idempotent: it replaces the top entry in `CHANGELOG.md` and `docs/CHANGELOG-DETAILED.md` rather than stacking a duplicate.

**Why:** Lets the maintainer iterate on a wrong or incomplete changelog draft by simply re-running the workflow instead of hand-editing or reverting.
