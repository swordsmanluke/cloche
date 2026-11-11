---
id: req-4e9a
status: active
scope:
    level: domain
    domains:
        - dras
        - container-runtime
        - security-safety
hints:
    - DRA cannot write to project
    - DRA needs git or make
    - pass env var to DRA
    - unknown container profile
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/dras.md#the-locked-down-container
    extracted_at: 2026-11-11T19:30:05.271827581Z
    extracted_by: intent-scan
created: 2026-11-11T19:30:05.323460281Z
updated: 2026-11-11T19:30:05.323460281Z
---

DRA containers (container { profile = "dra" }) use their own cloche-dra image, a read-only project except notes.md, an enforced --internal network with an egress sidecar, no-new-privileges, and ignore CLOCHE_EXTRA_MOUNTS and CLOCHE_EXTRA_ENV; an unknown profile name fails the step and never falls back to an unrestricted container.

**Why:** A DRA is a long-lived agent, the riskiest kind, so it gets tighter limits than a workflow run.
