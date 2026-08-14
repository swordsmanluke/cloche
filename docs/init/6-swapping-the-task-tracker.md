# 6. Swapping the Task Tracker

Beads is the default, not a requirement. The daemon only ever sees one
interface: **the `list-tasks` workflow prints ready tasks as JSONL**.
Everything tracker-specific lives in five generated scripts — replace them
and you've replaced the tracker.

## The contract

`get-tasks.py` (run by the `list-tasks` workflow) must print zero or more
lines to stdout, one JSON object per line:

```json
{"id": "gh-42", "title": "Add /health endpoint", "description": "…", "status": "open"}
```

- `id` — stable identifier; the daemon passes it back to every step as the
  `CLOCHE_TASK_ID` environment variable.
- `status` — only `"open"` tasks are dispatched (other statuses: `in-progress`,
  `closed`).
- `title` / `description` — whatever your prompt-building step needs.
- Print nothing (exit 0) when there's no ready work — the loop idles.
- Exit non-zero only for real errors; that surfaces as a `list-tasks` failure.

(The parsed schema lives at `internal/host/task.go` if you want the source
of truth.)

## The scripts to replace

| Script | Obligation to your tracker |
|--------|---------------------------|
| `get-tasks.py` | List ready work as the JSONL above. Don't list tasks another run already claimed. |
| `claim-task.py` | Mark `$CLOCHE_TASK_ID` as in-progress so it stops appearing in get-tasks. |
| `prepare-prompt.py` | Fetch the task's content and publish it for the agent (keep the file + `task_prompt_path` pattern from tutorial 4). |
| `close-task.py` | Mark the task done after its change merges. |
| `unclaim.py` | Reset the task to open (it also stops the loop — keep that). |

`merge.py` and `cleanup.py` are tracker-agnostic; leave them alone.

## Example: GitHub Issues sketch

```bash
# get-tasks: open issues labeled "cloche", newest first
gh issue list --label cloche --state open --json number,title,body \
  | jq -c '.[] | {id: ("gh-" + (.number|tostring)), title, description: .body, status: "open"}'

# claim-task:  gh issue edit "${CLOCHE_TASK_ID#gh-}" --add-label in-progress
# close-task:  gh issue close "${CLOCHE_TASK_ID#gh-}"
# unclaim:     gh issue edit "${CLOCHE_TASK_ID#gh-}" --remove-label in-progress
```

Claiming via a label is a convention: get-tasks must then also filter out
issues carrying the `in-progress` label, or two loop passes could dispatch
the same issue.

Any language works — the workflow just runs a command. Update the `run =`
lines in `.cloche/host.cloche` if you rename the scripts, and check
`cloche validate` afterwards.

That's the end of the series — [workflows.md](../workflows.md) is the full
DSL reference when you're ready to reshape the workflows themselves.
