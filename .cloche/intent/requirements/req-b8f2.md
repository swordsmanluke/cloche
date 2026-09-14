---
id: req-b8f2
status: active
scope:
    level: domain
    domains:
        - security-safety
hints:
    - container step needs NET_ADMIN capability
    - should I run the container with --privileged
    - agent needs elevated permissions inside its container
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#docker-network-isolation
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.455002404Z
updated: 2026-09-14T15:33:28.455002404Z
---

Never run Cloche containers with `--privileged` or extra capabilities like `NET_ADMIN` unless there is a specific, justified need. The base image runs as an unprivileged `agent` user.

**Why:** Cloche containers don't need elevated capabilities for normal agent work; granting them widens the blast radius of a compromised or misbehaving agent step.
