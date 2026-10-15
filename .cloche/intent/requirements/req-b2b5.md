---
id: req-b2b5
status: active
scope:
    level: project
hints:
    - is it done if tests pass
    - closing a ticket after green tests
    - acceptance criteria check before close-task
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/spikes/2026-09-17-intent-ab-pilot/README.md#Follow-up: recommendation tickets, worked by Haiku (2026-09-20/21)'
    extracted_at: 2026-10-15T15:57:31.706145393Z
    extracted_by: intent-scan
created: 2026-10-15T15:57:31.754147026Z
updated: 2026-10-15T15:57:31.754147026Z
---

Pass/fail of the test suite is not proof a ticket's acceptance criteria are met; Cloche's pipeline only gates on tests, so verify the diff against the ticket's acceptance criteria before closing.

**Why:** In the Haiku pilot, 4 of 5 tickets closed with incomplete or absent acceptance-criteria matches despite green tests (e.g. cloche-8od1 closed on a non-fix and had to be reopened).
