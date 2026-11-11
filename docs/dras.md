# DRAs (Domain Responsible Agents)

A DRA owns responsibility for one domain of a project (authentication, the
engine, performance...). Design: `docs/plans/2026-11-10-autonomous-loop-design.md`,
Part 5. This page covers the definition on disk; sessions and wake-ups come later.

## Layout

```
.cloche/dras/<name>/
  charter.md   # required: the domain, what it may do, the skills it uses
  notes.md     # the DRA's memory; created empty if missing
```

Zero DRAs is the default and changes nothing. The directory name is the
domain, and follows the same rule as a label: lowercase letters, digits and `-`.

## Notes and git

**Cloche never commits `notes.md`.** Whether notes are tracked in git or
ignored (e.g. `.cloche/dras/*/notes.md` in `.gitignore`) is the project's choice.

## Commands

- `cloche dra list [--project <dir>]` prints each DRA's name, whether a session
  exists (`-` until sessions are implemented) and the charter's first heading.
- `cloche validate` reports a DRA with no `charter.md` or an invalid name.
