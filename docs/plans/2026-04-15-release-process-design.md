# Release Process Design

**Date:** 2026-04-15
**Status:** Design

## Problem

Cloche has no release concept today. The `main` host workflow bumps
`internal/version/VERSION` on every completed task, and pushing to `origin/main`
is the implicit "release". There are no git tags beyond a historical `v0.1.0`,
no CHANGELOG, and no way for downstream alpha users to tell a stable version
from a WIP HEAD — or to learn which commits broke their `.cloche/` configs.

## Solution

Add two lightweight host workflows that run on demand:

1. **`changelog`** — walks `<last-release-tag>..HEAD`, filters out per-task
   version-bump commits and auto-generated `cloche run` log commits, runs an
   agent to draft summary + detailed changelogs (with breaking-change callouts
   for DSL / CLI / protocol touches), then prepends the drafts to `CHANGELOG.md`
   and `docs/CHANGELOG-DETAILED.md` and commits them. The maintainer reviews
   the committed result and either amends manually or re-runs the workflow;
   re-runs are idempotent per version (the top entry is replaced, not stacked).
2. **`release`** — verifies the top `CHANGELOG.md` entry matches the current
   `VERSION`, creates an annotated `v<VERSION>` tag, pushes main + tag, and
   creates a GitHub Release via `gh`.

Neither workflow bumps the version. Per-task build bumps in the existing `main`
workflow continue unchanged; maintainers may manually run `bumpver minor|major`
before invoking `changelog` when a release warrants a clean number. A one-time
bootstrap tags the current `origin/main` HEAD with its `VERSION` so the first
`changelog` run has a well-defined baseline. Every subsequent run diffs from
the latest `v*` tag.

## Design Details

### One-time bootstrap

Run once, by hand, before the first `changelog` invocation:

```sh
git fetch origin
BOOT_SHA=$(git rev-parse origin/main)
BOOT_VERSION=$(git show "$BOOT_SHA:internal/version/VERSION")
git tag -a "v$BOOT_VERSION" \
  -m "Bootstrap tag — baseline for the release process" "$BOOT_SHA"
git push origin "v$BOOT_VERSION"
gh release create "v$BOOT_VERSION" --title "v$BOOT_VERSION" \
  --notes "Bootstrap release. Changelog entries begin with the next release."
```

### `changelog` host workflow

Added to `.cloche/host.cloche`:

```
workflow "changelog" {
  host {
    agent_command = "claude"
  }

  step guard {
    run     = "bash .cloche/scripts/changelog-guard.sh"
    results = [success, fail]
  }

  step collect-commits {
    run     = "bash .cloche/scripts/changelog-collect-commits.sh"
    results = [success, fail]
  }

  step draft {
    prompt  = file(".cloche/prompts/draft-changelog.md")
    timeout = "15m"
    results = [success, fail]
  }

  step commit {
    run     = "bash .cloche/scripts/changelog-commit.sh"
    results = [success, fail]
  }

  guard:success           -> collect-commits
  guard:fail              -> abort
  collect-commits:success -> draft
  collect-commits:fail    -> abort
  draft:success           -> commit
  draft:fail              -> abort
  commit:success          -> done
  commit:fail             -> abort
}
```

### `release` host workflow

```
workflow "release" {
  host {}

  step guard {
    run     = "bash .cloche/scripts/release-guard.sh"
    results = [success, fail]
  }

  step tag {
    run     = "bash .cloche/scripts/release-tag.sh"
    results = [success, fail]
  }

  step publish {
    run     = "bash .cloche/scripts/release-publish.sh"
    results = [success, skipped, fail]
  }

  guard:success   -> tag
  guard:fail      -> abort
  tag:success     -> publish
  tag:fail        -> abort
  publish:success -> done
  publish:skipped -> done
  publish:fail    -> abort
}
```

### Scripts

All scripts live under `.cloche/scripts/` and run via `sh -c` from the main git
worktree (host-workflow semantics — see `docs/workflows.md`). Run-scoped
artifacts live under `$(cloche get temp_file_dir)`, following the pattern used
by `merge-to-base.sh`.

**`changelog-guard.sh`** — Preconditions for drafting: working tree clean;
branch is `main`; at least one `v*` tag exists (otherwise the bootstrap
snippet hasn't been run); the filtered commit corpus is non-empty.

**`changelog-collect-commits.sh`** — Commit corpus:

- `LAST_TAG = git tag -l 'v*' | sort -V | tail -1`.
- Raw list from `git log --pretty=format:'%H%x09%s' "$LAST_TAG..HEAD"`.
- Drop commits whose subject matches `^Version [0-9]+\.[0-9]+\.[0-9]+$`
  (per-task version bumps) or
  `^cloche run [a-z0-9]+-[a-z-]+: .* \((succeeded|failed)\)$`
  (auto-generated run logs).
- Write filtered SHAs to `$TEMP/commits.txt`, one per line. Append a `!`
  marker to any SHA that touches a path in the **breaking-change watchlist**:
  - `internal/dsl/**`
  - `internal/protocol/**` (protos and generated code)
  - `docs/workflows.md`
  - `cmd/cloche/**`, `cmd/clo/**` (CLI surfaces)
  - `.cloche/scripts/**`, `.cloche/prompts/**`, `.cloche/host.cloche`,
    `.cloche/Dockerfile` (shipped scaffold)
- For each filtered SHA, write `$TEMP/diffs/<short-sha>.patch` via
  `git show --stat --patch`.
- Write `$TEMP/release_version.txt` (from `internal/version/VERSION`) and
  `$TEMP/last_tag.txt` (from `$LAST_TAG`).
- Fail with "nothing to release since v<LAST_TAG>" if the filtered list is
  empty; include the count of elided noise commits.
- `cloche set release_temp_dir "$TEMP"` so later steps locate artifacts.

**`changelog-commit.sh`** — Persists the agent-drafted files:

- `VERSION = cat internal/version/VERSION`; `DATE = date -u +%Y-%m-%d`.
- Ensures `CHANGELOG.md` exists (creates with `# Cloche Changelog` header if
  not). Same for `docs/CHANGELOG-DETAILED.md` with
  `# Cloche Detailed Changelog`.
- **Idempotent insert**: if the top entry in `CHANGELOG.md` is already
  `## v<VERSION> —`, replaces that section (heading through the next
  `## v` heading or EOF) with the new content. Otherwise prepends a new
  `## v<VERSION> — <DATE>` section after the file header. Same rule applied
  to `docs/CHANGELOG-DETAILED.md`.
- `git add CHANGELOG.md docs/CHANGELOG-DETAILED.md && git commit -m
  "Changelog for v<VERSION>"`.

**`release-guard.sh`** — Preconditions for publishing: working tree clean; on
`main`; tag `v<VERSION>` does not exist locally or on origin; top entry in
`CHANGELOG.md` matches `## v<VERSION> — ` (fails with "run `cloche run
--workflow changelog` first — top entry is for v<OTHER>" otherwise); unless
`CLOCHE_RELEASE_DRY_RUN=1`, `gh auth status` succeeds.

**`release-tag.sh`** — Extracts the body of the top `CHANGELOG.md` entry
(between `## v<VERSION>` and the next `## v` heading) into
`$TEMP/release_notes.md`, then `git tag -a "v<VERSION>" -F
"$TEMP/release_notes.md"`. Writes `release_notes_path` to the KV store.

**`release-publish.sh`** — If `CLOCHE_RELEASE_DRY_RUN=1`, emits
`CLOCHE_RESULT:skipped`. Otherwise runs:

```sh
git push origin main
git push origin "v$VERSION"
gh release create "v$VERSION" --title "v$VERSION" \
  --notes-file "$(cloche get release_notes_path)"
```

### Agent prompt: `.cloche/prompts/draft-changelog.md`

The agent:

- Reads `$(cloche get release_temp_dir)/commits.txt` and every patch in
  `./diffs/`.
- Reads `internal/version/VERSION` and the most recent entries already in
  `CHANGELOG.md` so voice and format stay consistent.
- Classifies each commit into **Feature**, **Fix**, **UI/UX**, **Internal**
  (omitted from summary), or **Breaking**.
- Commits with a `!` marker MUST be examined for DSL / CLI / protocol
  breakage. Not every `internal/dsl/` change is user-visible (e.g. a parser
  refactor isn't) — the agent decides case-by-case.
- Writes two files in `$TEMP`:
  - **`CHANGELOG-summary.draft.md`** — user-facing. Sections in order:
    **Breaking changes** (each with a one-line migration note naming the
    affected DSL keyword / CLI flag / config file), **Features** (with links
    to relevant `docs/` files when they exist), **Notable fixes**. Omits
    Internal. Terse, one bullet per item.
  - **`CHANGELOG-detailed.draft.md`** — full list grouped by Feature / Fix /
    UI/UX / Breaking / Internal. Each bullet cites the 7-char SHA.
- For each Feature, searches `docs/` for a matching design doc
  (`docs/plans/*-<slug>-design.md`) and includes a relative link when found.
- Does not invent changes. A commit whose intent is unclear goes under
  Internal with a `(uncertain — please review)` note.
- Does not include a `## v<VERSION> — <date>` heading — `changelog-commit.sh`
  owns the heading format.

### Bookkeeping files

- **`CHANGELOG.md`** (new, repo root) — summary changelog, reverse
  chronological. Populated by the first `changelog` run.
- **`docs/CHANGELOG-DETAILED.md`** (new) — full detail including fixes,
  UI/UX, and internal churn.

### Docs update

Add a **Cutting a release** section to `CLAUDE.md` documenting:

- The two-phase flow: `cloche run --workflow changelog` → inspect the
  committed CHANGELOG → `cloche run --workflow release`.
- Optional manual bump before `changelog`: `bumpver minor|major && git add
  internal/version/VERSION && git commit -m "Version <new>"`.
- Dry-run: `CLOCHE_RELEASE_DRY_RUN=1 cloche run --workflow release`.
- Recovery: if `tag` succeeds but `publish` fails (e.g. network error), the
  local tag and commit remain. Retry by hand with `git push origin main &&
  git push origin v<VERSION> && gh release create v<VERSION> --notes-file
  <notes>`.

### Error Handling

- Guard scripts fail fast with actionable messages: dirty tree, wrong
  branch, missing bootstrap tag, version mismatch, duplicate tag, missing
  `gh` auth.
- `changelog-collect-commits.sh` fails friendly on empty corpus with
  "nothing to release since v<LAST_TAG>", citing how many commits were
  elided as noise.
- `changelog-commit.sh` is idempotent per version — re-runs replace the top
  entry rather than duplicating it, so the maintainer can iterate on the
  draft.
- `release-publish.sh` has an explicit `skipped` wire driven by
  `CLOCHE_RELEASE_DRY_RUN`, so a full release can be rehearsed without side
  effects.
- If `release-tag.sh` succeeds but `release-publish.sh` fails partway
  through, the local tag is left in place and the maintainer recovers by
  hand per the `CLAUDE.md` note.

## Alternatives Considered

**`dev` → `main` branch model.** A long-lived `dev` branch with merges to
`main` as the release event was rejected as excessive process for a
single-maintainer alpha. Tags on a single branch convey the same
"stable vs WIP" distinction with no branching overhead.

**Standalone shell script.** Bypassing the workflow system with a single
`scripts/release.sh` was rejected because the workflow system is Cloche's
own dogfood — running the release through `cloche run` exercises the host
workflow surface and keeps the release process inspectable via `cloche
status`/`logs` like any other run.

**First-class `cloche release` CLI subcommand.** Rejected as too heavy for
an alpha. A host workflow requires no Go code, no gRPC endpoint, and no
release of the release tooling itself.

**Auto-bump at release time.** Rejected in favor of keeping per-task build
bumps as-is. The maintainer retains control: optionally run `bumpver
minor|major && commit` before `changelog` when the release warrants a clean
number; otherwise the release inherits whatever build number has accumulated.

**Explicit `BREAKING:` commit prefix.** Rejected because existing commits
are not tagged, and requiring future discipline adds friction for a
single-dev workflow. The agent-plus-watchlist approach handles historical
commits and catches breakage the author forgot to flag.

**Single combined workflow.** An earlier draft had `release` do changelog
generation, review, tagging, and publishing in one shot. Split into two so
the maintainer can iterate on the changelog draft (re-running `changelog`
is cheap and idempotent) before committing to a tagged release.

## Verification

1. **Bootstrap.** Run the snippet in §Bootstrap once. Verify with
   `git tag -l 'v*'` and `gh release list`.

2. **Dry-run the changelog workflow.** `cloche run --workflow changelog`.
   Expect: guard passes; `commits.txt` excludes all `Version ...` and
   `cloche run ...` commits; agent produces both draft files; editor opens
   each in sequence; after closing, `CHANGELOG.md` and
   `docs/CHANGELOG-DETAILED.md` exist with a `## v<VERSION>` section; a
   commit `Changelog for v<VERSION>` is present. Inspect the draft — it
   should flag protocol-adjacent commits like `29b1425` for review.

3. **Idempotency.** Re-run `changelog` with no new commits. Expect: the top
   entry is replaced, not duplicated.

4. **Dry-run release.** `CLOCHE_RELEASE_DRY_RUN=1 cloche run --workflow
   release`. Expect: guard passes; local tag created; `publish` emits
   `skipped`; `gh release list` unchanged. Cleanup: `git tag -d v<VERSION>`.

5. **Real release.** Optionally bump minor first, re-run `changelog`, then
   `cloche run --workflow release`. Verify tag, GitHub Release body, and
   `origin/main` state.

6. **Negative tests.** Dirty tree → `changelog-guard` fails. Release before
   changelog → `release-guard` fails with version mismatch. Release with a
   duplicate tag → `release-guard` fails. Changelog with zero substantive
   commits → `collect-commits` fails with "nothing to release".
