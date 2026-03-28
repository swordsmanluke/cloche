# Write Documentation Fixes

Read the documentation audit report and fix every issue it identifies.

## Process

1. Retrieve the report path:
   ```
   clo get doc_report_path
   ```
2. Read the full report.
3. For each inaccuracy listed in the report:
   - Open the referenced doc file.
   - Fix the specific error to match source code reality.
   - Re-read the file after editing to confirm the fix is correct.
4. For each missing-documentation entry:
   - If it's a missing CLI command: add it to `docs/USAGE.md` in the appropriate section, following the existing format.
   - If it's a missing subsystem: add a section to the relevant existing doc (system design or USAGE.md) rather than creating a new file, unless the subsystem is large enough to warrant its own doc.
   - If it's a missing DSL feature: add it to `docs/workflows.md` in the appropriate section.
5. After all fixes, re-read each modified file to verify correctness.

## Rules
- Match the tone and format of existing documentation — do not introduce a new style.
- Do not invent behavior not present in source code. When writing new documentation, grep the source to confirm details.
- Do not reorganize or rewrite sections that are already correct.
- Keep changes minimal and targeted — fix what the report says, nothing more.
- Report success when all report items have been addressed.
- Report fail only if a file write actually failed.
