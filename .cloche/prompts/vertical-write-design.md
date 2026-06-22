# Vertical workflow: write the design document

The design branch has been checked out. Write a thorough design document for this
feature, commit it, and record the path in KV.

## Read the feature

```bash
bd show "$CLOCHE_TASK_ID" --json | jq -r '.[0]'
```

Study the title, description, and any acceptance criteria carefully. If there are
existing child layer tasks, read them too (`bd list --parent "$CLOCHE_TASK_ID" --all`).

## Research the codebase

Before writing, read the relevant code to understand:
- What exists that the feature builds on
- What interfaces, types, or commands the feature will add or change
- Where the new code will live and what patterns it should follow

Use grep and file reading to explore. Write an *informed* design, not a generic one.

## Write the document

Create `docs/plans/<YYYY-MM-DD>-<feature-id-slug>.md` (use today's date for
`YYYY-MM-DD` and a short kebab-case slug derived from the feature title):

```markdown
# <Feature title>

**Date:** YYYY-MM-DD
**Status:** Draft
**Feature task:** `<CLOCHE_TASK_ID>`

## Problem

<2–4 sentences: what problem does this feature solve and why does it matter now?>

## Solution overview

<High-level description: what changes, what gets added, what gets removed. Enough
for a reviewer to understand the approach without reading code.>

## Key design decisions

<Numbered list of significant choices with brief rationale. Include trade-offs
the reviewer might want to push back on.>

## Implementation plan

<Sketch of the layer order. For each layer: what it ships, what it mocks.
Example:
- L1 — DSL/CLI surface: adds `foo` command; mocks the resolver
- L2 — Resolver: real implementation; mocks the store
- L3 — Store: replaces mock with SQLite adapter>

## Risks and trade-offs

<What could go wrong, what this approach gives up, known limitations.>

## Open Questions for Reviewer

<Numbered list of genuine open questions — things you could not answer from
the code or the ticket. Each question should be specific and actionable.
If you have no open questions, write "None.">
```

The `## Open Questions for Reviewer` section is extracted verbatim as the PR body,
so make it actionable for the reviewer, not a placeholder.

## After writing

1. Commit the new document:
   ```bash
   git add docs/plans/
   git commit -m "Add design doc for $CLOCHE_TASK_ID: <feature-title>"
   ```

2. Record the doc path in KV:
   ```bash
   cloche set design_doc_path "docs/plans/<your-filename>"
   ```

3. Output:
   ```
   CLOCHE_RESULT:success
   ```

## Hard constraints

- Do not change status to `Approved` — the reviewer does that when they merge the PR.
- Do not touch any source code or existing docs.
- Keep the design doc under 200 lines. Brevity over completeness for a design review.
