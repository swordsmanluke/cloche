# Review the Change

Tests pass. Your job is to find a case where this change is still wrong, and prove it.

The task this change is meant to do:

```
cat /workspace/$(clo get task_prompt_path)
```

The change is the difference between the workspace and its base commit: run
`git status` and `git diff HEAD`, and read new (untracked) files in full. Read the
code the change touches too: callers of changed functions, and the tests.

## What counts as a finding

Only a failing case counts. For each problem, do one of:

- write a test that fails because of it (preferred), and leave the test in place; or
- name the exact input or situation, and the wrong result it gives.

Look for: wrong results and missed edge cases, a caller the change broke, missing
checks on outside input, lost errors or data, and parts of the task that were not
done. Do not report style, naming, or anything you cannot show failing. If the
change is sound, say so; do not invent problems.

Do not fix the code yourself. The only files you may add or change are tests that
show a finding.

## Reporting findings

If you have findings, write them to `/tmp/review-findings.md`, one per item: the file
and line, the failing test's name or the breaking input, and what goes wrong. Then
record the path so the fix step reads it:

```
clo set review_findings_path /tmp/review-findings.md
```

Report `fail` only when that file holds at least one finding with its evidence.
Otherwise report `success`.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — you found no failing case
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you found at least one failing case and wrote it to the findings file

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
