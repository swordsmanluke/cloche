---
id: req-49c7
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - does cloche-agent need the onnx runtime
    - why is embedding logic only in the daemon
    - adding semantic selection to the CLI or in-container agent
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#embedding-backends
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461286601Z
updated: 2026-09-15T00:41:34.461286601Z
---

Only cloched links an embedder; cloche, clo, and cloche-agent stay pure Go — semantic requirement selection happens exclusively in the daemon, never in-container or in the short-lived CLI.

**Why:** Keeps the CLI/agent/in-container binaries free of ML runtime dependencies (e.g. ONNX Runtime).
