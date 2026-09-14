# Discover the Project Domain Map

Maintain `.cloche/intent/domains.yaml`, the map of this project's major
architectural systems. This is what lets injected requirements be scoped to
the part of the codebase a task actually touches instead of firehosed at
every prompt.

## Setup

```bash
DOMAINS_FILE="$CLOCHE_PROJECT_DIR/.cloche/intent/domains.yaml"
```

## Mode

- **If `$DOMAINS_FILE` does not exist** (first scan): do a full survey. Read
  the top-level directory layout, `internal/*/` package names and their doc
  comments, `cmd/*/` binaries, and `docs/plans/*design*.md` files to
  understand the project's major systems. Propose 5–12 domains — enough to
  be useful for scoping, not so many that they're noise. Prefer domains that
  map to a package or a small cluster of related packages over one domain
  per file.
- **If `$DOMAINS_FILE` already exists** (incremental scan): read it first.
  Propose only additions for genuinely new top-level packages or doc areas
  that don't fit an existing domain, and description touch-ups for domains
  whose scope has visibly drifted from their `paths`. Leave everything else
  alone.

## Hard rule: respect `user_edited`

A domain entry may carry `user_edited: true` — a human wrote or corrected
it. **Never modify or remove a `user_edited: true` domain's `name`,
`description`, or `paths`.** You may still add new, separate domains.

## Format

```yaml
version: 1
domains:
  - name: workflow-dsl
    description: The .cloche workflow DSL — parser, validation, step types, wiring.
    paths: ["internal/dsl/**", "docs/workflows.md"]
  - name: versioning
    description: Version string management, release process, changelog.
    paths: ["internal/version/**", "docs/plans/*release*"]
    user_edited: true
```

- `name` — short, kebab-case, stable (it's referenced by requirements'
  `scope.domains`; renaming one orphans existing scoping until the next
  scan reconciles it — prefer adding a new domain over renaming unless the
  old name is clearly wrong).
- `description` — one sentence, specific enough to disambiguate from
  neighboring domains.
- `paths` — glob patterns (relative to the project root) covering the
  domain's source and docs.
- Omit `user_edited` on domains you write or touch yourself; only a human
  edit (via the dashboard or `cloche intent`) sets it to `true`.

## Results

- Emit `CLOCHE_RESULT:success` once `$DOMAINS_FILE` is written (or, on an
  incremental scan, confirmed to need no changes).
- Emit `CLOCHE_RESULT:fail` if the project layout can't be read or the file
  can't be written.
