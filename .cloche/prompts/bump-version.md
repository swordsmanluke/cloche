# Bump Version

Determine the appropriate version bump for the changes just merged to the base branch, then update the version file.

## Version file

`internal/version/VERSION` — contains a single line: `major.minor.build`

## Versioning policy

- **Build**: increment for routine bug fixes, internal refactors, chores — no API or user-facing behavior changes.
- **Minor**: backward-compatible API updates, new features, new CLI commands or flags, new gRPC endpoints.
- **Major**: backward-incompatible changes, removing or deprecating APIs, incompatible gRPC API changes, removing `cloche` commands.

When bumping **minor**, reset build to 0. When bumping **major**, reset minor and build to 0.

## Process

1. Run `git log --oneline -1` to see the most recent commit (the one just merged).
2. Run `git diff HEAD~1 --stat` to see what files changed.
3. If needed, run `git diff HEAD~1 -- <specific files>` to inspect the actual changes.
4. Read the current version from `internal/version/VERSION`.
5. Decide the bump level based on the versioning policy above.
6. Write the new version to `internal/version/VERSION` (single line, no trailing newline beyond what's there).
7. Commit the version bump: `git commit -am "bump version to X.Y.Z"`

## Rules

- Always bump at least the build number. Every merge gets a version bump.
- If the main workflow failed (`CLOCHE_MAIN_OUTCOME` is not `succeeded`), report success without bumping — there's nothing to version.
- Report **success** after committing the bump. Report **fail** only if git operations fail.
