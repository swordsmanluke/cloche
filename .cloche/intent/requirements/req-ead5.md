---
id: req-ead5
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - agent-adapters
hints:
    - SessionStart hook failing in container
    - agent using wrong model in container
    - what prompt did the agent actually get
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: ed9aa322eff97fafec02c735f6a1a1191cef93cd
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.455119768Z
updated: 2026-10-15T15:54:45.455119768Z
---

Workflow containers receive only .credentials.json and a settings.json reduced to env/apiKeyHelper/forceLogin*/aws*/otelHeadersHelper; the host's settings.local.json and hooks/model/permissions are never copied in. Every agent step records its exact assembled prompt as <step>.prompt.md beside its transcript. Interactive consoles keep the full host settings.

**Why:** Copying the host settings whole ran host-path SessionStart hooks that failed in the container and made model/effort/permissions silently follow the host into autonomous agents.
