Review the code changes made in this workspace.

## Review Criteria
- **Correctness**: Does the code do what it's supposed to? Are there logic errors, off-by-one mistakes, or unhandled edge cases?
- **Architecture**: Do changes follow the hexagonal architecture (domain/ports/adapters)? Are concerns properly separated?
- **Tests**: Are new behaviors covered by tests? Do existing tests still make sense after the changes?
- **Error handling**: Are errors propagated correctly? Are failure modes handled gracefully?
- **Concurrency**: Are shared resources protected? Could there be race conditions?
- **Naming**: Are names clear, consistent with existing conventions, and self-documenting?

## Guidelines
- Read the git diff to understand what changed
- Focus on substantive issues, not style nitpicks
- If you find a bug or correctness issue, explain the failure scenario
- If the code looks good, say so briefly — don't invent problems

## Output
Provide a summary of findings. If there are issues, list each with:
1. File and location
2. What the problem is
3. Suggested fix
