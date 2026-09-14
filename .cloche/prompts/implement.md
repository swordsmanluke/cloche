# Implement Feature

Implement the following change in this project. Retrieve the task description by running:

```
cat /workspace/$(clo get task_prompt_path)
```

If you cannot find that file or it is empty ABORT and fail.

## Guidelines
- Follow existing project conventions if files already exist
- Write tests for new functionality
- You MUST run `go test ./... 2>&1` before reporting success. If any tests fail, fix the code until tests pass. Only report success when all tests pass. If you cannot get tests to pass after reasonable effort, report fail.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:{{ $result_nonce }}:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:{{ $result_nonce }}:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
