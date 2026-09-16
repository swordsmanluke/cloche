---
id: req-3ef8
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - why doesn't this layout bug show up in the JS unit tests
    - adding a test for a CSS/flexbox clipping bug in the console
    - getBoundingClientRect returning zero in a dashboard test
    - should this console test go in npm test or somewhere else
    - console dashboard test requires installing a browser
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: f8ffffaae5823325b3c79694744d3c4d9b5879fd
    extracted_at: 2026-09-16T15:50:00Z
    extracted_by: intent-scan
created: 2026-09-16T15:44:38.033456446Z
updated: 2026-09-16T15:44:38.033456446Z
---

Layout regression tests for the console dashboard (e.g. flexbox-shrink/clipping bugs) must run in a real browser via Playwright, not jsdom, and are kept out of the default `npm test` run.

**Why:** jsdom's non-rendering DOM always returns zeroed getBoundingClientRect values, so it cannot catch real layout bugs like flex-shrink clipping. These tests require `npx playwright install firefox` (plus system libraries on a bare Linux host), so they're run explicitly via `npm run test:e2e` instead of being bundled into `npm test`.
