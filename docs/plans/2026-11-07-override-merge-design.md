# Overrides: Merging Config Files, and Keeping Overrides Out of Result Branches

**Date:** 2026-11-07
**Status:** Accepted (CLOCHE-A98).
**Decision record:** Saga feature `cloche/override-merge`, batch CLOCHE-B21.
Answer ids (`CLOCHE-Ann`) are cited inline.

## Problem

`.cloche/overrides/` is copied on top of `/workspace/` at container start
(`internal/adapters/docker/runtime.go:254`). Each file replaces the project's file at
the same path. That's the right behaviour for files like `CLAUDE.md`, but it doesn't
work for config files where an override only wants to change a few keys.

The case that prompted this is enabling a Claude Code plugin in some projects'
containers only. The plugin is installed in the project's Dockerfile, and it has to
be enabled with project-scope settings, because cloche replaces the image's
`~/.claude/settings.json` at start and removes `enabledPlugins` from it. This was
tested on 2026-11-07 with `ponytail@ponytail`:

```dockerfile
RUN claude plugin marketplace add DietrichGebert/ponytail \
 && claude plugin install ponytail@ponytail
```

```json
// .cloche/overrides/.claude/settings.json
{ "enabledPlugins": { "ponytail@ponytail": true } }
```

This works, but the override replaces the project's whole `.claude/settings.json`.
Any permissions, hooks or env the project keeps there are lost inside the container,
unless they are copied into the override by hand and kept in sync from then on.

A second problem came up while looking at this. **Override files leak into result
branches.** Extraction copies all of `/workspace` back into the worktree and runs
`git add -A` (`internal/adapters/docker/extract.go:256`). It restores only the
tracked files that `.clocheignore` kept out of the container. Nothing undoes the
overrides. So the scaffold's `overrides/CLAUDE.md`, or the plugin `settings.json`
above, gets committed to `cloche/<run-id>` and goes on to merge into `main`.

## Goals

- An override file can **merge** keys into the project's config file instead of
  replacing it.
- Existing overrides **work exactly as they do now**. Merging is opt-in per file.
- A file that won't parse **fails the run**. Cloche never quietly falls back to
  copying a settings file.
- Files that overrides wrote **never reach a result branch** unless the agent itself
  changed them.

## Non-goals

- INI and other formats without a single standard. They can be added when a project
  needs one.
- Merging files outside `/workspace` (for example the image's `~/.claude/settings.json`).
  Overrides only target the workspace.
- Keeping comments, key order or formatting in merged files. The merged file only
  exists inside the container (see Part 1).
- Appending to lists.

## Part 1: Keep overrides out of result branches (bugfix, build bump)

This ships first, as its own change (CLOCHE-A99). It fixes a bug that exists today, and Part 2
relies on it: a merged, re-serialised config file must not land in a branch.

### At container start

While applying `.cloche/overrides/` (and, after Part 2, merges), cloche records a
manifest: one entry per workspace path it wrote, with the SHA-256 of the content
written there.

```json
{ "version": 1,
  "files": { ".claude/settings.json": "sha256:…", "CLAUDE.md": "sha256:…" } }
```

The manifest is written into the container at `/etc/cloche/overrides.json`, outside
`/workspace`, owned by root with mode 0644. The agent runs as `agent` and can read
the file but not change it. Extraction already reads from the container, so keeping
the manifest there avoids adding host-side run state. The container is still around
when extraction runs.

### At extraction

After the container tree is copied into the worktree and before `git add -A`, cloche
reads the manifest from the container. For each path listed:

- **If the file still has the hash cloche recorded:** the agent didn't touch it.
  Cloche restores it from `BaseSHA` with `git checkout BaseSHA -- <path>`. If the
  path didn't exist at `BaseSHA` (the override created it), cloche deletes it.
- **If the hash differs:** the agent edited the file, so the change was presumably
  deliberate. It's kept and committed as normal (CLOCHE-A101).
- **If the file is gone:** the agent deleted it. That's handled the same way as any
  other deletion.

For multi-repo extraction (`ContainerSubPath`), each worktree handles only the
manifest paths under its subpath, with the subpath prefix removed.

If the manifest is missing (containers started before this change), cloche logs it
and skips the restore, which is today's behaviour. If the manifest is present but
can't be read, extraction fails. A silent skip would commit override content without
anyone noticing.

The restore step uses the existing `restoreFromBase` helper.

## Part 2: Merge overrides (feature, minor bump)

### Marking a file for merge

Inside `.cloche/overrides/`, a file whose name has `.merge` immediately before its
final extension is merged into the matching target rather than copied (CLOCHE-A100):

| Override file | Target in `/workspace` | Behaviour |
|---|---|---|
| `.claude/settings.json` | `.claude/settings.json` | copy (unchanged) |
| `.claude/settings.merge.json` | `.claude/settings.json` | merge |
| `config/app.merge.yaml` | `config/app.yaml` | merge |
| `pyproject.merge.toml` | `pyproject.toml` | merge |

The marker goes before the extension, as in `docker-compose.override.yml`. Editors
still recognise the file type, and cloche picks the format from the real extension.
The cost is that a real file literally named `x.merge.json` can't be copied as a plain
override.

Supported extensions are `.json`, `.yaml`/`.yml` and `.toml`. A `.merge` file with any
other extension is an error.

It is also an error for one directory to contain both `foo.json` and `foo.merge.json`.

### Merge semantics

The merge follows JSON Merge Patch (RFC 7386), applied to the parsed documents
whatever the file format:

- If both values are maps, they merge recursively.
- Any other value in the patch (scalar or list) replaces the target's value. Lists
  are replaced, not appended to.
- `null` in the patch deletes the key.
- The top level of both documents must be a map. Anything else is an error.

YAML writes `null` as `null` or `~`. TOML has no null, so a TOML merge file can't
delete keys. That's not supported for now (CLOCHE-A103).

### What gets merged with what

The target is the project file as it was copied into the container, so `.clocheignore`
and the run's repo selection are respected:

- If the target was copied in, the merge file is merged into it.
- If the target doesn't exist, or wasn't copied in (it's `.clocheignore`d or outside
  the selected repos), the merge file is merged into an empty document. The result is
  the merge file with its `null` keys removed. Cloche never reads a host file the
  container wasn't meant to see (CLOCHE-A102).

Merging happens on the host, in a staging directory, and the result is copied in with
the plain overrides. Nothing runs inside the container. The merged file's hash is
recorded in the Part 1 manifest, so at extraction an untouched merged file is restored
from the base commit like any other override. That covers merges with no target too:

- **The target wasn't in the base commit:** the file is deleted from the result
  branch, so the branch doesn't gain it.
- **The target was in the base commit but not copied into the container** (for
  example, it's `.clocheignore`d): the file is restored from the base commit, so the
  merged content never replaces the real file.

### Failure

Each of these fails the run at container start with a message that names the file,
and never falls back to copying:

- the merge file or its target doesn't parse
- either document's top level isn't a map
- the extension isn't supported
- the merge file and a plain override target the same file
- a YAML file contains more than one document

`cloche validate` makes the same checks against the project's working tree, so
mistakes show up before a run. Interactive consoles (`cloche console`) apply the
same merges.

### Serialisation

| Format | Library | Notes |
|---|---|---|
| JSON | `encoding/json` | `Decoder.UseNumber()` so large integers survive. 2-space indent, sorted keys. |
| YAML | `gopkg.in/yaml.v3` (already a dependency) | Anchors, tags and comments are lost. |
| TOML | `github.com/BurntSushi/toml` (already a dependency) | Datetimes stay typed. |

Losing formatting is acceptable because the merged file only exists inside the
container, and Part 1 keeps it out of result branches.

### Code

- `internal/adapters/docker/overrides.go` (new): walks the overrides directory, sorts
  files into copy and merge, parses, merges, writes a staging tree, and returns the
  manifest entries.
- `runtime.go`: replaces the single `docker cp overridesDir/.` with copying the staged
  tree, then writes the manifest.
- `extract.go`: reads the manifest and restores untouched files before `git add -A`.
- `cmd/cloche` validate: runs the same parse and merge checks without copying anything.
- Tests: a table of merge cases per format (nested maps, list replacement, null
  delete, missing target, ignored target, parse errors, copy/merge conflict), plus
  extraction tests for untouched, edited, created and deleted override files.

### Docs

- `docs/USAGE.md`: the overrides section and the project layout tree.
- `docs/SAFETY.md`: the overrides audit note. Merge files are committed too, so the
  same rule applies: no credentials.
- `docs/agent-setup-claude.md`: the per-project plugin recipe, using
  `.claude/settings.merge.json`.

## Resolved questions

1. Deleting keys from TOML: not supported for now (CLOCHE-A103). A sentinel string
   can be added later if a project needs it.
2. When the agent edits a file that an override wrote, the edit is kept and
   committed (CLOCHE-A101).
