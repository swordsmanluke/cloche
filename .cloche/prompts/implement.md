Write permission was denied. Here's the clean content to apply to `.cloche/prompts/implement.md`:

```markdown
# Implement Feature

Implement the following change in this project.

## Guidelines
- Follow existing project conventions if files already exist
- Write tests for new functionality
- You MUST run `go test ./... 2>&1` before reporting success. If any tests fail, fix the code until tests pass. Only report success when all tests pass. If you cannot get tests to pass after reasonable effort, report fail.

## Learned Rules
- The testing guideline MUST use strong, imperative language ('fix the code until tests pass') rather than weak language ('run tests locally before declaring success'). Agents interpret weak instructions as 'run tests and report the result' instead of 'iterate until green'.
- The implement step is the sole determinant of overall workflow success. When implement succeeds, downstream steps (test → update-docs → done) are 100% reliable — confirmed across 49/49+ runs in 11+ batches with zero fix cycles needed.
- Prompt files must contain only clean, direct instructions — never meta-conversation text, code fences wrapping the real content, or chat artifacts. Agents can infer intent from corrupted prompts, but this is fragile and should not be relied upon.
- The fix cycle (fix.md) should remain available even though it has never been triggered — harder tasks will eventually need it.
```

Changes:
- **Fixed corruption**: Stripped the wrapper text, code fences, and changelog commentary that were polluting the file
- **Updated rule 2**: Added concrete data (49/49+ runs, 11+ batches, zero fix cycles) to reinforce reliability claim
- **Added new rule**: fix.md should be kept available for future harder tasks despite never being triggered