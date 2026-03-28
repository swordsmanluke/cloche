# Check for Missing Documentation

Identify cloche commands and subsystems that lack documentation entirely.

## Process

1. Retrieve the report path from the previous step:
   ```
   clo get doc_report_path
   ```
2. Read the existing report at that path.
3. Scan for undocumented CLI commands:
   - List all registered subcommands in `cmd/cloche/` (look for cobra command registrations or similar patterns).
   - Compare against commands documented in `docs/USAGE.md`.
   - Flag any command present in source but absent from usage docs.
4. Scan for undocumented subsystems:
   - Walk the `internal/` directory structure to identify major subsystems (adapters, ports, engine, host, dsl, protocol, etc.).
   - Check whether each subsystem has corresponding coverage in design docs (`docs/plans/`) or the system design doc.
   - Flag subsystems with no documentation at all.
5. Check for undocumented workflow DSL features:
   - Grep the DSL parser for supported keywords, block types, and built-in functions.
   - Compare against what `docs/workflows.md` covers.
6. Append a new section to the report with all missing documentation findings.

## Append Format

Add to the existing report:

```markdown
## Missing Documentation

### Undocumented Commands
- `cloche <command>` — no usage documentation found

### Undocumented Subsystems
- `internal/<package>/` — no design or reference documentation

### Undocumented DSL Features
- `<keyword>` — supported by parser but not in workflows.md
```

## Rules
- Read the report path via `clo get doc_report_path` — do not hardcode the path.
- Append to the existing report; do not overwrite previous findings.
- Only flag genuinely missing docs, not thin docs. If a command has at least a one-line description in USAGE.md, it counts as documented.
- Report success after updating the report.
- Report fail only if you could not read/write the report or scan the source.
