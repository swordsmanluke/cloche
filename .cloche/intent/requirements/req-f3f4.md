---
id: req-f3f4
status: active
scope:
    level: domain
    domains:
        - container-runtime
        - dras
hints:
    - cloche-dra:latest is missing
    - make install skipping cloche-dra image
    - where does the DRA image get saga
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: g4d6-main
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.869057363Z
updated: 2026-11-11T21:04:22.869057363Z
---

The cloche-dra image takes `saga` from the host binary (`command -v saga`, overridable via SAGA_BIN) copied into a git-ignored build context; when it is unavailable `make docker-dra` skips with an explanatory message rather than faking the image, and the Dockerfile verifies the binary with `saga --help`.

**Why:** No saga release tarball exists; a binary that cannot run in the image should fail the build, not the first DRA wake-up.
