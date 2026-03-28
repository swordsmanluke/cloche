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
4. Write the report to `.cloche/doc-report.md`.
5. Store the report path so later steps can find it:
   ```
   clo set doc_report_path .cloche/doc-report.md
   ```

## Report Format

```markdown
# Documentation Audit Report

## docs/USAGE.md
- **Line ~N**: Says X but source shows Y
- ...

## docs/workflows.md
- ...

## Summary
- N errors found across M files
```

## Rules
- Only flag concrete, verifiable inaccuracies — not style or tone issues.
- Every finding must reference the specific doc file and the source location that contradicts it.
- If a doc file is fully accurate, note that briefly and move on.
- Report success after writing the report, even if zero errors were found.
- Report fail only if you were unable to complete the audit (e.g., could not read files).
