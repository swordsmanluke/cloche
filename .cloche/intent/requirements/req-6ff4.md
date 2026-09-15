---
id: req-6ff4
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - should we just use keyword search instead of embeddings
    - is the keyword embedder good enough
    - no embedder configured, relying on keyword fallback
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#embedding-backends
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461179678Z
updated: 2026-09-15T00:41:34.461179678Z
---

Keyword-overlap retrieval is a degraded fallback for intent requirement selection, not an equal alternative to a real embedder — the project's own spike measured embeddings roughly doubling recall@3 over keyword overlap.

**Why:** Documents why the embedder adapter chain prefers onnx/ollama over keyword matching.
