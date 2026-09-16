---
id: req-a45d
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - project-config
hints:
    - cloche project shows the wrong Dockerfile for a subdirectory
    - setting up an isolated experiment inside the main repo
    - does a nested folder get its own project config
    - sandboxing an experimental workflow in the same repo
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: dcd8d3aa029ae2ce04f710853969587d423564b5
    extracted_at: 2026-09-16T20:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:58:52.347455439Z
updated: 2026-09-16T19:58:52.347455439Z
---

Cloche resolves a project's identity (config, Docker image, active-run tracking) at the git repository root regardless of branch or worktree — a subdirectory inside the main repo (e.g. an experiments/ folder) can never get independent project identity and always inherits the repo root's config and Dockerfile.

**Why:** Confirmed when `cloche project` showed the wrong Dockerfile/image for every run dispatched from a nested experiments/intent-ab/harness subdirectory that was set up expecting its own project identity; the working fix was same-project, differently-named workflows at the repo root instead of a nested project.
