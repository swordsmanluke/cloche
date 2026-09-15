---
id: req-bcec
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - can I just link to fonts.googleapis.com for the console
    - adding a webfont to the dashboard
    - console fonts don't load without internet access
    - where do custom fonts live in the web dashboard
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: sexn-main
    extracted_at: 2026-09-15T17:47:23Z
    extracted_by: intent-scan
created: 2026-09-15T17:48:49.61448954Z
updated: 2026-09-15T17:48:49.61448954Z
---

Console fonts (IBM Plex Mono / IBM Plex Sans) are self-hosted as vendored .woff2 files under internal/adapters/web/static/fonts/, not loaded from Google Fonts or any other CDN at runtime.

**Why:** The console-restyle task explicitly required this: "Self-host or vendor the fonts; the console must not depend on a network fetch at runtime." The follow-up commit added the woff2 files directly into the web adapter's static assets rather than a <link> to fonts.googleapis.com.
