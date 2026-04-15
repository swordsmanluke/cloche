# Draft a Changelog

Generate two changelog drafts — a user-facing summary and a detailed full
listing — for the set of commits since the last release tag. A human
maintainer will review and edit the drafts before they are committed, so
prefer accuracy over flourish: if you're unsure about a commit, say so.

## Inputs

Retrieve the run-scoped workspace:

```bash
TEMP=$(cloche get release_temp_dir)
```

Then read:

- `$TEMP/commits.txt` — one commit per line, `<sha>\t<subject>` (possibly
  with a trailing ` !` marker, see **Watchlist** below).
- `$TEMP/diffs/<short-sha>.patch` — the diff for each listed commit.
- `$TEMP/release_version.txt` — the version this release targets
  (`MAJOR.MINOR.BUILD`).
- `$TEMP/last_tag.txt` — the previous tag the range is measured from.
- `$CLOCHE_PROJECT_DIR/CHANGELOG.md` — existing changelog (if any). Read
  the top few entries so your voice and formatting match what's already
  shipped.
- `$CLOCHE_PROJECT_DIR/docs/` — look here for design docs to link from
  Features (see **Doc links**).

## Classification

Classify every commit in `commits.txt` into exactly one of:

- **Feature** — new user-facing capability (new CLI command or flag, new
  DSL keyword, new workflow type, new dashboard, etc.).
- **Fix** — a bug fix that users could plausibly have hit.
- **UI/UX** — output formatting, help text, error messages, ergonomics.
  Worth mentioning in the detailed log but usually not the summary.
- **Breaking** — any change that could require users to edit their
  `.cloche/` files, update their commands, or re-generate something.
  Breaking changes go in BOTH the summary and detailed log.
- **Internal** — refactors, test changes, build plumbing, proto
  regenerations, dependency bumps, doc-only changes. Omit from the
  summary; include in the detailed log under its own section.

When classification is genuinely ambiguous from the commit message and
diff, place the commit under **Internal** and append
`(uncertain — please review)` to its detailed-log bullet.

## Watchlist

Commits with a trailing ` !` marker in `commits.txt` touch paths that are
part of Cloche's user-visible surface:

- `internal/dsl/**` — DSL parser, grammar
- `internal/protocol/**` — gRPC protos (wire format)
- `docs/workflows.md` — DSL reference docs
- `cmd/cloche/**`, `cmd/clo/**` — CLI commands
- `.cloche/scripts/**`, `.cloche/prompts/**`, `.cloche/host.cloche`,
  `.cloche/Dockerfile` — shipped scaffold

A `!` marker is a prompt to check, not a verdict. Not every change in
these paths is user-visible (e.g. a parser refactor that preserves
syntax, a proto field rename on the generated side). Read the diff and
decide. When a flagged commit IS user-visible, classify it as
**Breaking** (or **Feature** if it's additive and non-breaking) and in
the summary include a one-line migration note naming the affected DSL
keyword, CLI flag, or config key — e.g. `The 'agent_args' config key was
renamed to 'agent_flags'; update your .cloche/*.cloche files.`

## Outputs

Write exactly two files, overwriting any previous contents:

### 1. `$TEMP/CHANGELOG-summary.draft.md`

User-facing summary. Sections in this order, omitting any that are empty:

```
### Breaking changes

- <commit subject or reframed sentence>. Migration: <what the user must do>.

### Features

- <commit subject or reframed sentence>. <optional doc link>

### Notable fixes

- <commit subject or reframed sentence>.
```

- Do NOT include a `## v<VERSION> — <date>` heading — the commit script
  owns that.
- Do NOT include Internal changes here.
- Omit **Notable fixes** entirely if every Fix in the detailed log is
  trivial (typos, tiny internal regressions that users wouldn't have
  hit). The summary should be skimmable.
- If after classification there are no Features, Fixes, or Breaking
  changes to show, write a single line: `- No user-facing changes in
  this release.`

### 2. `$TEMP/CHANGELOG-detailed.draft.md`

Full listing grouped by category. Include every commit from `commits.txt`.

```
### Breaking

- `<short-sha>` <one-line description>. Migration: <...>.

### Features

- `<short-sha>` <one-line description>.

### Fixes

- `<short-sha>` <one-line description>.

### UI/UX

- `<short-sha>` <one-line description>.

### Internal

- `<short-sha>` <one-line description>.
```

- Use 7-character SHAs.
- One bullet per commit. Reframe cryptic commit subjects into a sentence
  a user could understand, but don't invent specifics the diff doesn't
  support.
- Omit any section whose list is empty.
- Again: no top-level `## v...` heading — the commit script adds it.

## Doc links

For each **Feature** bullet, search `$CLOCHE_PROJECT_DIR/docs/` for a
design doc that matches. The naming pattern is
`docs/plans/YYYY-MM-DD-<slug>-design.md`. If you find one, append a
Markdown link to the bullet, e.g. `. ([design](docs/plans/2026-04-15-release-process-design.md))`.
Use a relative path from the repo root. Do not invent links — only
include paths you've verified exist.

## Voice and length

- Match the tone of the existing entries in `CHANGELOG.md` if any are
  present. If none, aim for terse, factual release-note style:
  imperative or declarative, no marketing language.
- One sentence per bullet in the summary. One sentence per bullet in the
  detailed log.
- Do not praise the change ("cleaner", "much better"). State what it is.

## Results

- Emit `CLOCHE_RESULT:success` once both files have been written.
- Emit `CLOCHE_RESULT:fail` if you cannot read the inputs, cannot write
  the outputs, or encounter a corrupt commit/diff that blocks
  classification.
