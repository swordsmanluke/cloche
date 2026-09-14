---
id: req-38df
status: active
scope:
    level: domain
    domains:
        - security-safety
hints:
    - does network_allow actually block container network access
    - container reached a site not in my network_allow list
    - how do I really restrict what a container can reach on the network
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/SAFETY.md#network-allowlisting
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454567863Z
updated: 2026-09-14T15:33:28.454567863Z
---

`network_allow` in a workflow's `container {}` block is parsed and stored but not enforced at runtime — containers currently run with unrestricted network access. Declare it anyway as documentation of intent, and use Docker-level network controls for actual enforcement today.

**Why:** Until runtime enforcement lands, Docker-level controls (baking dependencies into the image, restricting DNS, etc.) are the only real egress restriction; the declared allowlist is forward-looking documentation, not a working gate.
