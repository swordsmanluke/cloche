---
id: req-d642
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - should I edit the doc or fix the code to match
    - doc finding says code and docs disagree
    - document workflow rewrote something to match a bug
    - is this a doc bug or a code bug
    - make the doc match the source is the wrong repair
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: runs/3lcz-main/task_prompt.md
    extracted_at: 2026-09-15T18:01:18Z
    extracted_by: intent-scan
created: 2026-09-15T18:02:25.907811069Z
updated: 2026-09-15T18:02:25.907811069Z
---

The document workflow must not fix a finding that means the code is wrong (not the doc) by editing the docs to match the code; write-docs needs an escape hatch to mark such findings as needs-code-change and leave the docs untouched.

**Why:** A previous run rewrote the correct swordsmanluke URLs to match a wrong go.mod module path and recorded it in the changelog as an org rename that never happened — the workflow made the docs agree with a bug because it had no way to say the fix belonged in code, not in the docs.
