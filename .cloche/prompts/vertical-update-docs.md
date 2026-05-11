# Vertical workflow: update project documentation

All implementation layers for the feature have been approved. The docs branch has
been checked out for you, based on the bottom-most layer's branch (so the full
feature code is present in your working tree).

## Scope

**Project docs only.** Inline code comments and godoc were updated during layer
implementation; do NOT edit Go source files.

Files to consider updating:

- `docs/` — anything in here that touches the feature's surface area
- `README.md` — if user-visible behavior changed
- `CHANGELOG.md` and `docs/CHANGELOG-DETAILED.md` — add an entry for the feature
- `docs/USAGE.md`, `docs/INSTALL.md`, etc. — if relevant
- DSL/API reference files — if the feature changed those

If a doc file mentions behavior that this feature changed, update it. If a doc
file describes something the feature replaced, replace the description.

## Process

1. Read the feature's full diff:
   ```bash
   base=$(clo get vertical_base_branch 2>/dev/null || echo main)
   git diff "$base..HEAD" --stat
   ```
2. Walk the changed source files and identify which doc files cover them.
   Use `grep -r "<keyword>" docs/` to find doc references that may now be wrong.
3. Edit only the sections that are actually out of date.
4. Re-read each edited file to confirm it is valid markdown / consistent with the
   rest of the doc.

## Hard constraints

- **No code changes.** If you find a code-level doc that needs updating
  (godoc, comment), make a note in your commit message and stop. Don't edit it
  — the layer that changed the code should have. Surface the gap so the user can
  decide whether to push it back.
- **No invented behavior.** Document only what the source code actually does.
- **Be terse.** Doc edits should be the minimum needed to reflect the change.
- **CHANGELOG entry is mandatory.** Add at least one bullet under the unreleased
  section (or whatever this project's convention is — match existing entries).

## If there is genuinely nothing to document

If you compare the diff against existing docs and nothing needs to change beyond
a CHANGELOG bullet, that is fine. Add the CHANGELOG entry and report success. The
docs PR will be small but correct.

Commit your changes to the current branch with a clear message like
`docs: <feature title>`.

## PR description

Before exiting, write a focused PR description to
`$(clo get temp_file_dir)/pr-description.md`. The host's open-docs-pr step picks
this up verbatim as the PR body, so make it specific:

1. **What changed in the docs** — list each file you edited with a one-line
   summary of the section you touched (e.g., "`docs/workflows.md`: added the
   `repository` block to the DSL reference under 'Top-level blocks'").
2. **Anything you noticed but did NOT update** — a stale godoc comment, an
   outdated example in a code file, etc. State it so the user can decide; do not
   silently fix it (the layer that touched the code should have).
3. **CHANGELOG entry** — quote the bullet you added so the reviewer doesn't have
   to dig.

Keep it tight; usually 10-20 lines is right for a docs PR.
