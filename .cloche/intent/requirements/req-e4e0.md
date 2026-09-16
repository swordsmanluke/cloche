---
id: req-e4e0
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - ollama client tracebacks with no result marker
    - socket timeout not caught by except URLError
    - local model slow to first byte
    - bonsai/ollama requests timing out at 120s
    - TimeoutError not converted to OllamaError
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: ab02a7b10caf94b3fa3807ffc9dc06c4d08ea188
    extracted_at: 2026-09-16T15:18:21Z
    extracted_by: intent-scan
created: 2026-09-16T15:20:04.829873732Z
updated: 2026-09-16T15:20:04.829873732Z
---

The intent-ab harness's Ollama client must use a long request timeout (600s, not 120s) for local reasoning models, and must catch socket-level TimeoutError/OSError explicitly alongside urllib.error.URLError, since Python's urllib does not wrap raw socket timeouts in URLError.

**Why:** Real local-model latency on full task prompts exceeds 120s to first byte; a bare socket TimeoutError bypassed the OllamaError handling path and tracebacked to exit 1 with no result marker in a real pilot run.
