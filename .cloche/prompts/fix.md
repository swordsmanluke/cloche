# Fix Validation Failures

Fix the code based on the validation failures below.
Only modify files that need fixing. Do not rewrite the entire project.

## Review findings

If a review step flagged this change, its findings are at the path given by
`clo get review_findings_path` (the key is unset when there are none). Read that file
if present: each finding names a failing test or breaking input. Fix the code so
those cases pass; keep the reviewer's failing tests and do not delete or weaken them.
If a finding is wrong, say why in your output rather than ignoring it.

You MUST run `go test ./... 2>&1` after making changes. If tests still fail, keep fixing until they pass. Only report success when all tests pass.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
