# Investigate Documentation Accuracy

Audit the project's documentation against the actual source code. Your goal is to find every place where docs are wrong, outdated, or misleading.

## Process

1. Read the full list of documentation files under `docs/` (USAGE.md, workflows.md, INSTALL.md, SAFETY.md, web-dashboard.md, agent-setup-claude.md, agent-setup-codex.md, and any others).
2. For each doc file, spawn subagents to validate claims against source code:
   - CLI flags and subcommands described in USAGE.md — grep `cmd/cloche/` for actual flag definitions and subcommand registrations.
   - Workflow DSL syntax described in workflows.md — check `internal/dsl/` parser for actual supported syntax.
   - Configuration options — check source for actual config keys, defaults, and valid values.
   - Architecture descriptions — verify package structure and interfaces match what's documented.
   - Installation steps — verify build commands and dependencies are current.
3. Collate all findings into a single Markdown report listing every error or mismatch found, organized by doc file.
4. If `.cloche/doc-report.md` already exists (check `git log --follow -- .cloche/doc-report.md` or read the working copy before you overwrite it), note which of its unresolved findings still apply. A finding that matches one already present and unresolved in the previous report is recurring — mark it as such (see Report Format) instead of writing it up as new.
5. Write the report to `.cloche/doc-report.md`, replacing the previous contents.
6. Store the report path so later steps can find it:
   ```
   clo set doc_report_path .cloche/doc-report.md
   ```

## Report Format

Every finding gets an `Outcome:` line. At this stage — before anything has been
fixed — it is always `unresolved`; the `write-docs` step is responsible for
updating it once it knows what actually happened. Leaving the placeholder in
place gives `write-docs` a fixed spot to edit rather than requiring it to
restructure the report.

```markdown
# Documentation Audit Report

## docs/USAGE.md
- **Line ~N**: Says X but source shows Y
  - Outcome: unresolved
- ...

## docs/workflows.md
- ...

## Summary
- N errors found across M files
```

If a finding recurs from the previous report (step 4 above), say so in the
finding itself, e.g. `- **Line ~N**: Says X but source shows Y (recurring —
also flagged in the prior report and still unresolved)`.

## Rules
- Only flag concrete, verifiable inaccuracies — not style or tone issues.
- Every finding must reference the specific doc file and the source location that contradicts it.
- If a doc file is fully accurate, note that briefly and move on.
- Report success after writing the report, even if zero errors were found.
- Report fail only if you were unable to complete the audit (e.g., could not read files).

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
