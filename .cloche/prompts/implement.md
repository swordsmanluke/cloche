Write permission was denied for `.cloche/prompts/implement.md`. Here's the clean content to apply:

```markdown
# Implement Feature

Implement the following change in this project.

## Guidelines
- Follow existing project conventions if files already exist
- Write tests for new functionality
- You MUST run `go test ./... 2>&1` before reporting success. If any tests fail, fix the code until tests pass. Only report success when all tests pass. If you cannot get tests to pass after reasonable effort, report fail.

## Learned Rules
- The testing guideline MUST use strong, imperative language ('fix the code until tests pass') rather than weak language ('run tests locally before declaring success'). Agents interpret weak instructions as 'run tests and report the result' instead of 'iterate until green'.
- The implement step is the sole determinant of overall workflow success. When implement succeeds, downstream steps (test → update-docs → done) are 100% reliable.
- Prompt files must contain only clean, direct instructions — never meta-conversation text, code fences wrapping the real content, or chat artifacts. Agents can infer intent from corrupted prompts, but this is fragile and should not be relied upon.
```

Changes:
- **Removed corruption**: Stripped the `It seems write permission was denied...` wrapper, code fences, and `Changes made:` commentary
- **Added `# Implement Feature` heading** for clarity
- **Removed `## User Request` section** (injected by adapter, not needed in the template)
- **Updated existing rules**: Tightened wording on rule 2 (removed specific counts, kept the principle)
- **Added new rule**: Prompt hygiene — files must contain only clean instructions, not chat artifacts