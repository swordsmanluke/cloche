---
id: req-8fff
status: active
scope:
    level: domain
    domains:
        - agent-adapters
        - daemon-host-orchestration
hints:
    - step failed although the agent finished
    - agent printed text after result marker
    - result marker not found
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: vmr8-main
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745363188Z
updated: 2026-11-11T17:52:17.745363188Z
---

A CLOCHE_RESULT marker is accepted if it is the last valid marker carrying the step's nonce anywhere in the final output, not only on the last line; the nonce is what stops quoted or injected markers from counting. Both the prompt adapter and host-side parsing must agree.

**Why:** An agent printed more text after its marker (background-task notification) and a finished step was treated as failed.
