# Built-in Agents

Cloche has a concept of **known agents** — agent commands it understands natively. Known
agents receive default arguments automatically, and some arguments are required and will
always be injected regardless of `agent_args`. Unknown agents (e.g. `codex`) receive no
default arguments; the prompt is passed on stdin and the command is invoked as-is.

## Known Agents

| Command | Default args | Required args |
|---------|-------------|---------------|
| `claude` | `-p --dangerously-skip-permissions --model sonnet` | `--output-format stream-json --verbose` |

## How `agent_args` Interacts with Defaults and Required Args

When `agent_args` is **not** set, the agent receives its full default argument list.

When `agent_args` **is** set, it replaces the default args entirely — but required args
are always appended if not already present. You cannot remove a required arg.

For `claude`, this means:

- `-p`, `--dangerously-skip-permissions`, and `--model sonnet` are **overridable**:
  they are present by default but absent if you supply `agent_args` without them.
- `--output-format stream-json` and `--verbose` are **required**: Cloche injects them
  even if your `agent_args` omits them. `--output-format stream-json` is necessary
  because the prompt adapter parses Claude's streaming JSON output to extract results
  and token usage. `--verbose` is required for that stream to include the result event.

## `claude`

Claude Code is the default agent. If no `agent_command` is configured, `claude` is used.

### Default invocation

```
claude -p --output-format stream-json --verbose --dangerously-skip-permissions --model sonnet
```

The prompt is passed on stdin (via `-p`).

### Overriding arguments

Use `agent_args` to replace the default args. Required args (`--output-format stream-json`
and `--verbose`) are always present, so you do not need to include them.

**Example: use a different model**

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_args = "-p --dangerously-skip-permissions --model claude-opus-4-5"
  results = [success, fail]
}
```

**Example: add extra flags**

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  agent_args = "-p --dangerously-skip-permissions --model sonnet --max-turns 20"
  results = [success, fail]
}
```

Note that `-p` and `--dangerously-skip-permissions` must be included explicitly when
using `agent_args`, since they are not required args and will not be injected
automatically.

## Named Agent Declarations (`agent` block)

Rather than repeating `agent_args` on every step, you can declare named agents at the
workflow level and reference them by identifier. The `args` field in an `agent` block
maps directly to `agent_args`, so the same overridable/required rules apply.

**Example: per-model agents for different tasks**

```
workflow "develop" {
  agent haiku_claude {
    command = "claude"
    args    = "-p --dangerously-skip-permissions --model claude-haiku-4-5"
  }

  agent opus_claude {
    command = "claude"
    args    = "-p --dangerously-skip-permissions --model claude-opus-4-6"
  }

  step commit {
    prompt  = file(".cloche/prompts/commit.md")
    agent   = haiku_claude
    results = [success, fail]
  }

  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    agent   = opus_claude
    results = [success, fail]
  }

  implement:success -> commit
  implement:fail    -> abort
  commit:success    -> done
  commit:fail       -> abort
}
```

Required args (`--output-format stream-json` and `--verbose`) are still injected
automatically — you do not need to include them in `args`.

`-p` and `--dangerously-skip-permissions` are not required, so they must be included
explicitly if you want them (which you almost always do for `claude`).

Step-level `agent_command` and `agent_args` override the agent declaration. See
[USAGE.md — Agent Declarations](USAGE.md#agent-declarations) for the full resolution
order.

## Unknown Agents

Any `agent_command` value not listed in the table above is treated as an unknown agent.
Unknown agents:

- Receive no default arguments.
- Receive the prompt on stdin.
- Have no required args — `agent_args` is passed through verbatim.

See [How to Set Up Codex](agent-setup-codex.md) for a worked example.
