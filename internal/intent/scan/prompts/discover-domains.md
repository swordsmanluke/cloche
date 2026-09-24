# Discover the Project Domain Map

Identify this project's major architectural systems — the map that lets
injected requirements be scoped to the part of the codebase a task actually
touches instead of firehosed at every prompt. You write a plain CSV list;
a script turns it into `.cloche/intent/domains.yaml`. Never write or edit
`domains.yaml` yourself.

## Setup

```bash
DOMAINS_FILE="$CLOCHE_PROJECT_DIR/.cloche/intent/domains.yaml"
OUT="$(cloche get temp_file_dir)/domains.csv"
```

## Mode

- **If `$DOMAINS_FILE` does not exist, or `$CLOCHE_INTENT_FULL` is set**
  (first scan, or a forced full re-survey via `cloche intent scan --full`):
  do a full survey. Read the top-level directory layout, `internal/*/`
  package names and their doc comments, `cmd/*/` binaries, and
  `docs/plans/*design*.md` files to understand the project's major systems.
  Propose 5–12 domains — enough to be useful for scoping, not so many that
  they're noise. Prefer domains that map to a package or a small cluster of
  related packages over one domain per file.
- **Otherwise** (`$DOMAINS_FILE` already exists and no forced full
  re-survey): read it first. Keep every existing domain, and add only
  genuinely new top-level packages or doc areas that don't fit an existing
  domain, plus description touch-ups for domains whose scope has visibly
  drifted from their `paths`. If nothing needs to change, write nothing and
  report `none`.

Domains marked `user_edited: true` in `$DOMAINS_FILE` were written or
corrected by a human. Copy them into your list unchanged; the script keeps
them as they are regardless of what you write for them.

## Output

Write `$OUT` — the **full** list of domains that should exist (existing ones
you are keeping, plus any additions), as CSV with exactly this header:

```csv
name,description,paths
workflow-dsl,"The .cloche workflow DSL — parser, validation, step types, wiring.",internal/dsl/**;docs/workflows.md
versioning,"Version string management, release process, changelog.",internal/version/**;docs/plans/*release*
```

- `name` — short, kebab-case (`[a-z0-9-]`), stable: it's referenced by
  requirements' `scope.domains`, so prefer adding a new domain over renaming
  one.
- `description` — one sentence, specific enough to disambiguate from
  neighbouring domains.
- `paths` — one or more glob patterns relative to the project root,
  separated by `;`.
- Standard CSV quoting: wrap a field in double quotes if it contains a
  comma, a double quote (write it as `""`), or a line break. Nothing else is
  special — do not escape colons, backticks or anything else.

A script validates the file and reports back if anything is malformed; you
will get one chance to fix the CSV before the scan aborts.

## Results

- Emit `CLOCHE_RESULT:success` once `$OUT` is written.
- Emit `CLOCHE_RESULT:none` on an incremental scan where the existing map
  needs no changes (do not write `$OUT`).
- Emit `CLOCHE_RESULT:fail` if the project layout can't be read.
