Review the CLI source code and update usage documentation to reflect any changes.

## What to check
1. Read cmd/cloche/main.go and cmd/cloche/init.go to understand the current CLI surface
2. Compare against docs/USAGE.md

## Sections to keep in sync
- CLI Reference: subcommands, flags, usage examples
- Setting Up a New Project: scaffolding steps, workflow template
- Daemon Configuration: environment variables
- Build Commands: Makefile targets

## Rules
- Only modify docs/USAGE.md (and docs/workflows.md if workflow DSL syntax changed)
- Only make changes when there are actual discrepancies — do not rewrite for style
- If everything is already accurate, make no changes and report success
