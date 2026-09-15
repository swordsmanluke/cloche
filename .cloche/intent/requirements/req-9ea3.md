---
id: req-9ea3
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - changing intent.model in config.toml has no effect
    - how do I change the embedding model
    - intent embedder still uses default model
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#embedding-backends
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461091271Z
updated: 2026-09-15T00:41:34.461091271Z
---

The intent.model config key is not yet wired into embedder resolution; to override the onnx embedder's model, set the CLOCHE_INTENT_MODEL env var instead.

**Why:** Avoids wasted effort chasing a documented no-op config key.
