---
id: req-fb7d
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - how should the console look
    - what's the source of truth for the console redesign
    - console styling doesn't match the intended design
    - where's the design spec for the dashboard UI
    - picking colors/fonts for a new console component
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/design/console-restructured-mock.html
    extracted_at: 2026-09-15T17:47:23Z
    extracted_by: intent-scan
created: 2026-09-15T17:48:49.614323568Z
updated: 2026-09-15T17:48:49.614323568Z
---

Console UI redesign work must match docs/design/console-restructured-mock.html (the vendored "Mock 5" / .m5 reference, extracted from the "Three Shapes for Cloche" design artifact) precisely — exact colors, fonts, spacing and structure per its selectors — rather than approximating the look from a written description.

**Why:** The mock was vendored specifically so console-fidelity tickets have ground truth that doesn't require fetching the original artifact; every console-restructuring task prompt in this batch cites specific .m5 selectors, pixel values, and CSS variable names from it as the spec to hit exactly.
