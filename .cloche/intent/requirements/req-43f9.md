---
id: req-43f9
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
        - container-runtime
hints:
    - 'jq: command not found in container'
    - module test fails in run only
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 19dbb27879b957ee936a6fd2c43ea0f05611d82a
    extracted_at: 2026-11-11T19:21:22.374725056Z
    extracted_by: intent-scan
created: 2026-11-11T19:21:22.426092688Z
updated: 2026-11-11T19:21:22.426092688Z
---

Adding a tool that module scripts or their tests use (e.g. jq) means adding it to the agent image in .cloche/Dockerfile.

**Why:** Module scripts and tests failed in the container without it.
