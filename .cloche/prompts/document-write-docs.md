# Write Documentation Fixes

Read the documentation audit report and resolve every finding it identifies —
either by fixing the doc, or, where the doc is not actually the thing that's
wrong, by explicitly declining to touch it. The report you leave behind is the
only record of what happened in this run; it must let a reader see at a glance
what's still open without re-deriving anything from source.

## Process

1. Retrieve the report path:
   ```
   clo get doc_report_path
   ```
2. Read the full report.
3. For each finding (both inaccuracies and missing-documentation entries),
   first decide which side is wrong — see "Escape hatch" below. Most findings
   are ordinary doc errors:
   - Open the referenced doc file.
   - Fix the specific error to match source code reality.
   - Re-read the file after editing to confirm the fix is correct.
   - For missing-documentation entries specifically:
     - Missing CLI command: add it to `docs/USAGE.md` in the appropriate section, following the existing format.
     - Missing subsystem: add a section to the relevant existing doc (system design or USAGE.md) rather than creating a new file, unless the subsystem is large enough to warrant its own doc.
     - Missing DSL feature: add it to `docs/workflows.md` in the appropriate section.
4. After all fixes, re-read each modified file to verify correctness.
5. Update the report in place (see "Updating outcomes" below) and rewrite it
   to lead with the unresolved set (see "Report structure" below).
6. Before finishing, print the unresolved findings (needs-code-change and
   not-resolved) in your final step output — not just in the file — so
   they're visible in the run log even if nobody opens `doc-report.md`. If
   there are none, say so explicitly ("no unresolved findings this run").

## Escape hatch: when the doc isn't the thing that's wrong

Some findings look like a doc/source mismatch but are actually a code bug —
the report describes what the source currently does, and "make the doc match
the source" would just document the bug. A past run hit exactly this: a
finding showed `go.mod`'s module path disagreeing with the actual git remote,
and the run "fixed" it by rewriting the correct URLs elsewhere in the docs to
match the wrong module path, then logged it as an org rename that never
happened.

Before fixing a finding, ask: would fixing this mean asserting something
false about the *code* just to make the doc self-consistent? If the honest
fix requires changing source (e.g. `go.mod`, a flag definition, a default
value) rather than prose, that's out of scope for this workflow. In that
case:
- Do not edit the doc to paper over it.
- Mark the finding's outcome as `needs-code-change` (see below) with a short
  reason and, if one exists, a pointer to where the fix should land (a ticket
  ID, an issue, or a note that one should be filed).
- Leave the doc as-is.

When in doubt whether a mismatch is a doc bug or a code bug, treat it as a
code bug and use the escape hatch — an incorrectly-declined fix costs a
re-review; an incorrectly-forced doc edit asserts something false about the
codebase.

## Updating outcomes

Every finding in the report (from `investigate` and from `check-missing`) has
an `Outcome:` placeholder line reading `unresolved`. Replace each one with
exactly one of:

- `Outcome: resolved — <commit-time state>` — the doc was fixed; briefly
  describe what it now says (e.g. `Outcome: resolved — USAGE.md:131 no longer
  documents a repository block; verified no such syntax remains`), so a
  reader doesn't need to open the diff to know what changed.
- `Outcome: needs-code-change — <reason>` — the doc is not the bug; the
  source needs to change instead. Docs were left untouched. Include the
  pointer described above if one exists.
- `Outcome: not-resolved — <reason>` — you attempted the fix but couldn't
  complete it (e.g. ambiguous intent, conflicting source signals). Explain
  what's blocking it.

Do not leave any finding as `unresolved` — every one must land on one of the
three outcomes above.

## Report structure

After updating every outcome, rewrite `.cloche/doc-report.md` so it opens with
the unresolved set, then keeps the full per-file detail below for history:

```markdown
# Documentation Audit Report

## Unresolved Findings
- (none this run — everything resolved) — or —
- **docs/INSTALL.md**: go.mod module path vs git remote (needs-code-change — see below)

## docs/USAGE.md
- **Line ~N**: Says X but source shows Y
  - Outcome: resolved — ...

## docs/workflows.md
- ...

## Missing Documentation
### Undocumented Subsystems
- `internal/<package>/` — no design or reference documentation
  - Outcome: resolved — ...

## Summary
- N findings, M resolved, K needs-code-change, J not-resolved
```

Keep the "Unresolved Findings" entries short — one line each — and point back
to the full finding for detail rather than duplicating the reasoning.

## Rules
- Match the tone and format of existing documentation — do not introduce a new style.
- Do not invent behavior not present in source code. When writing new documentation, grep the source to confirm details.
- Do not reorganize or rewrite sections that are already correct.
- Keep doc changes minimal and targeted — fix what the report says, nothing more.
- Never edit source code (`.go` files, `go.mod`, etc.) to resolve a finding —
  that's what the escape hatch is for.
- Report success when every finding has a non-`unresolved` outcome recorded,
  regardless of whether that outcome was `resolved`, `needs-code-change`, or
  `not-resolved`.
- Report fail only if a file write actually failed.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
