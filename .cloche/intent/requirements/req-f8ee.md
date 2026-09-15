---
id: req-f8ee
status: active
scope:
    level: project
hints:
    - should I delete this old design doc
    - outdated plan doc still in the repo
    - marking a design doc superseded
    - should I update the changelog entry to reflect the corrected value
    - fixing a historical design doc to match reality
    - a plan doc references an outdated or wrong decision, do I edit it
    - cleaning up docs while fixing a bug that originated from a design doc
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: cciq-main
    extracted_at: 2026-09-15T17:55:00Z
    extracted_by: intent-scan
created: 2026-09-15T17:54:46.766548783Z
updated: 2026-09-15T17:54:46.766548783Z
---

Design docs under docs/plans/ and docs/CHANGELOG-DETAILED.md are append-only historical records. They are never deleted, and never rewritten to reflect a later correction — even when a fact they recorded turns out wrong (e.g. an incorrect module path or org name), the original text stays as-is. A superseding decision is captured by prepending a one-line note (e.g. "superseded") rather than editing the historical content in place.

**Why:** req-5446 already established that docs/plans/ design docs are never deleted, only marked superseded via a prepended note. A later task (fixing the go.mod module path github.com/cloche-dev/cloche, an org nobody owns) explicitly reinforced and broadened this: it was told not to rewrite docs/plans/2026-02-20-cloche-implementation.md or docs/CHANGELOG-DETAILED.md to reflect the corrected value, even though those files recorded the wrong value. This extends the rule to CHANGELOG-DETAILED.md and generalizes it from "never delete" to "never rewrite to match a later correction."
