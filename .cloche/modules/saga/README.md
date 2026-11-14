# saga

A `qa` backend over [Saga](the decision-record tool). Bind it:

```
bind qa = saga { env.SAGA_PROJECT = "cloche" }
```

`cloche questions ask|await|read` then run these workflows (`qa.cloche`):

| Workflow | `kv:` | `out:` | Behavior |
|---|---|---|---|
| `ask` | `questions_file` | `question_ids` | One `saga ask -p $SAGA_PROJECT -f <feature> --file <file> --json` batch; feature is the file's `backend.feature`. |
| `await-answer` | `question_ids` | `questions_open` | One non-blocking `saga show <id> --json` per id; open = no answer yet. |
| `read-answer` | `question_id` | `answer_path` | Writes the ruling in force to a file, a human's answer preferred over an agent's (the latest if none is human's). The file has a `Selected: <labels>` line for chosen options, then the text; either alone when only one is present. Fails when unanswered. |

`judge.md` and `asking.md` are served by `cloche questions guide [--judge]`.

## Prerequisites

`saga` and `jq` on the host `PATH`. `SAGA_BIN` overrides the binary. Saga's server is
`--server` or `$SAGA_URL`. No credentials in cloche.
