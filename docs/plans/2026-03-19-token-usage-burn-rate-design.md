# Token Usage and Burn Rate Design

**Date:** 2026-03-19
**Status:** In Progress — storage, query, and web UI layers implemented; capture, gRPC, and CLI layers pending

## Problem

Cloche has no visibility into token consumption. Users running orchestration
loops cannot tell how fast they are burning through their token budget. With
multiple agents (Claude, Codex) and multiple concurrent projects, there is no
way to answer "how many tokens/hour am I using?" at the project or global level.

## Solution

Add a two-layer system: an agent-specific **capture** layer that extracts token
counts after each prompt step, and a generic **query/reporting** layer that
aggregates usage across steps, runs, projects, and agents. Display burn rate
(tokens/hour) in `cloche status` and the web UI.

## Design Details

### Domain Types

New types in `internal/domain/usage.go`:

```go
// TokenUsage holds token consumption for a single agent step execution.
type TokenUsage struct {
    InputTokens  int64
    OutputTokens int64
}

// UsageSummary holds aggregated token usage with burn rate metrics.
type UsageSummary struct {
    TotalInputTokens    int64
    TotalOutputTokens   int64
    InputTokensPerHour  float64
    OutputTokensPerHour float64
}

// StepResult is the return value of AgentAdapter.Execute, combining the
// result string with optional token usage information.
type StepResult struct {
    Result string
    Usage  *TokenUsage
}
```

`StepExecution` gains an optional `Usage *TokenUsage` field.

### Adapter Interface

Extend `AgentAdapter.Execute()` to return a result struct:

```go
// internal/ports/agent.go

type AgentAdapter interface {
    Name() string
    Execute(ctx context.Context, step *domain.Step, workDir string) (domain.StepResult, error)
}
```

`StepResult` and `TokenUsage` are defined in `internal/domain/usage.go`.
All callers of `Execute()` update to use `domain.StepResult`. The generic
adapter returns `domain.StepResult{Result: result, Usage: nil}` since script
steps have no token usage.

### Capture: Claude Code

Claude Code's `--output-format stream-json` emits a final `result` event.
The prompt adapter (`internal/adapters/agents/prompt/prompt.go`) already parses
this stream. The result event JSON includes a `usage` field:

```json
{
  "type": "result",
  "result": "...",
  "usage": {
    "input_tokens": 12345,
    "output_tokens": 6789
  }
}
```

A separate `extractResultUsage()` function parses result events for usage data.
In the streaming path, `tryCommand` calls it on each scanned line and carries
the last non-nil usage forward. In the buffered path, `scanOutputForUsage`
scans the full output for a result event. Usage is returned as the third value
from `tryCommand` and propagated into the `domain.StepResult` returned by
`Execute()`.

### Capture: Codex

Codex does not emit usage in its output stream. Add an optional
`usage_command` config field to the prompt adapter (set via `host {}` block
or config.toml):

```toml
[agents.codex]
usage_command = "codex usage --last --json"
```

After a prompt step completes, if `usage_command` is configured, the adapter
runs it and parses the output as:

```json
{"input_tokens": 1234, "output_tokens": 567}
```

If the command fails or is not configured, usage is nil (graceful degradation).

### Capture: Other Agents

Agents that support neither stream-based nor command-based usage reporting
simply return `Usage: nil`. The query layer handles missing data gracefully —
burn rate calculations only include steps with known usage.

### Storage

Add columns to the `step_executions` table via schema migration:

```sql
ALTER TABLE step_executions ADD COLUMN input_tokens INTEGER DEFAULT 0;
ALTER TABLE step_executions ADD COLUMN output_tokens INTEGER DEFAULT 0;
ALTER TABLE step_executions ADD COLUMN agent_name TEXT DEFAULT '';
```

Update `SaveCapture()` in `internal/adapters/sqlite/store.go` to write these
columns when `TokenUsage` is non-nil.

### Query Layer

New methods on the `RunStore` port (`internal/ports/store.go`):

```go
type UsageQuery struct {
    ProjectDir string // empty = all projects
    AgentName  string // empty = all agents
    Since      time.Time
    Until      time.Time
}

type RunStore interface {
    // ... existing methods ...
    QueryUsage(ctx context.Context, q UsageQuery) ([]domain.UsageSummary, error)
}
```

SQLite implementation aggregates from `step_executions` joined with `runs`:

```sql
SELECT
    agent_name,
    SUM(input_tokens) AS input_tokens,
    SUM(output_tokens) AS output_tokens
FROM step_executions se
JOIN runs r ON se.run_id = r.id
WHERE r.project_dir = ? OR ? = ''
  AND se.agent_name = ? OR ? = ''
  AND se.completed_at >= ? AND se.completed_at <= ?
GROUP BY agent_name
```

The query returns one `UsageSummary` per agent. The caller computes
`BurnRate = TotalTokens / (WindowSeconds / 3600.0)`.

### gRPC

New RPC and messages in `cloche.proto`:

```protobuf
rpc GetUsage(GetUsageRequest) returns (GetUsageResponse);

message GetUsageRequest {
  string project_dir = 1; // empty = global
  string agent_name = 2;  // empty = all agents
  int64 window_seconds = 3; // 0 = all time
}

message GetUsageResponse {
  repeated UsageSummary summaries = 1;
}

message UsageSummary {
  string agent_name = 1;
  int64 input_tokens = 2;
  int64 output_tokens = 3;
  int64 total_tokens = 4;
  double burn_rate = 5; // tokens per hour
}
```

Also extend `StepExecutionStatus` with optional token fields:

```protobuf
message StepExecutionStatus {
  string step_name = 1;
  string result = 2;
  string started_at = 3;
  string completed_at = 4;
  int64 input_tokens = 5;
  int64 output_tokens = 6;
  string agent_name = 7;
}
```

### CLI: `cloche status`

**No args (project overview):** Append a burn rate line per agent:

```
Token usage (last 1h):
  claude   4,521 in / 2,103 out   6,624 total   ~18.2k/hr
  codex    1,200 in /   890 out   2,090 total    ~5.7k/hr
```

If no usage data exists, omit the section entirely.

**Task ID:** Show total tokens consumed by the task (all attempts):

```
Tokens: 8,714 (claude: 6,624 / codex: 2,090)
```

### Web UI

**Project dashboard:** Add a "Token Usage" card showing:
- Per-agent burn rate (tokens/hour, rolling 1h window)
- Per-agent total for last 24h
- Simple spark-line or bar showing rate over time (optional, future)

**Run detail page:** Show per-step token usage in the step execution table:
- New columns: Agent, Tokens (formatted as "in/out")
- Only shown for prompt steps with usage data

### Error Handling

- If stream-json parsing fails to extract usage, log a warning and continue
  with `Usage: nil`. Token tracking is best-effort, never blocks execution.
- If `usage_command` fails, log a warning and treat as unknown usage.
- If the `step_executions` migration fails, the daemon refuses to start (same
  as existing migration behavior).
- Division by zero in burn rate: if window is 0, burn rate is 0.

## Alternatives Considered

**Track cost in dollars.** Rejected — token pricing differs by provider, model,
and changes over time. Tokens are the stable unit; users can compute cost
externally.

**Separate usage table.** Rejected — usage is 1:1 with step executions. Adding
columns to the existing table is simpler and avoids joins for the common case
(display usage alongside step results).

**Poll agent APIs for usage.** Rejected for initial implementation — requires
API keys and provider-specific API clients. The stream-output and
post-command approaches work with the CLI tools already in use. Provider API
polling could be added as a future capture mechanism.

**Real-time token streaming during execution.** Rejected for now — Claude Code's
stream-json emits usage only at the end. Mid-step token counts would require
provider API polling, which is out of scope.
