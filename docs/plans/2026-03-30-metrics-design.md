# Code Quality Metrics Design

**Date:** 2026-03-30
**Status:** Design

## Problem

Cloche provides containerized environments and validated workflows for coding agents, but
has no built-in way to measure the quality of code that agents produce. Customers can wire
test steps to catch functional regressions, but structural quality issues — bloated files,
oversized functions, excessive complexity — go undetected unless the customer writes their
own tooling.

## Solution

Add code quality metrics as standard script steps that follow the existing result and
feedback conventions. A new `cloche-metric` binary ships in the container image alongside
`cloche-agent` and `clo`. It contains built-in metric implementations and a Language
Server Protocol (LSP) client for language-aware analysis. Metrics analyze only files
changed by the current run, report `pass`/`fail` via `CLOCHE_RESULT:`, and write detailed
findings to a KV key so downstream fix steps can read them through the existing feedback
mechanism.

No new step type is introduced. Metrics are script steps that call `cloche-metric`.
Customers can also write their own metric scripts following the same conventions.

## Design Details

### The `cloche-metric` Binary

A new Go binary at `cmd/cloche-metric/`. Like `clo`, it runs inside the container and
talks to the daemon via gRPC for KV access. It shares the version string from
`internal/version/VERSION`.

```
cmd/
  cloche-metric/    # New binary — metric runner
```

**Invocation:**

```
cloche-metric <metric-name> [flags]
```

The binary reads threshold configuration from the KV store (via `clo get`) and accepts
overrides from CLI flags. It:

1. Determines which files changed (see [Diff Detection](#diff-detection)).
2. Runs the requested metric against those files.
3. Writes a findings report to the KV store at key `metric_<step-name>` via `clo set`.
4. Writes the same findings report to stdout (captured as the step's `.log` file in
   `.cloche/output/`, making it available to the `feedback` mechanism).
5. Emits `CLOCHE_RESULT:pass` or `CLOCHE_RESULT:fail`.

### Built-in Metrics

Three metrics ship initially:

**`code-length`** — Flags files exceeding a line-count threshold.

No language server needed. Counts lines in each changed file and compares against
the configured maximum.

| Config key       | KV key                 | Default | Description                  |
|------------------|------------------------|---------|------------------------------|
| `max_file_lines` | `max_file_lines`       | 500     | Maximum lines per file       |

**`function-size`** — Flags functions exceeding a line-count threshold.

Requires a language server. Uses `textDocument/documentSymbol` to discover function
boundaries, then measures line counts.

| Config key           | KV key                 | Default | Description                     |
|----------------------|------------------------|---------|-------------------------------- |
| `max_function_lines` | `max_function_lines`   | 50      | Maximum lines per function      |

**`complexity`** — Flags functions exceeding a cyclomatic complexity threshold.

Requires a language server. Uses document symbols to locate functions, then analyzes
the function bodies for branching constructs (if/else, switch/case, loops, boolean
operators).

| Config key         | KV key              | Default | Description                       |
|--------------------|---------------------|---------|-----------------------------------|
| `max_complexity`   | `max_complexity`    | 10      | Maximum cyclomatic complexity     |

### DSL Usage

Metrics are standard script steps. Thresholds are step config keys, injected as
environment variables:

```
step check-size {
  run = "cloche-metric code-length"
  max_file_lines = 500
  results = [pass, fail]
}

step check-functions {
  run = "cloche-metric function-size"
  max_function_lines = 60
  results = [pass, fail]
}

step check-complexity {
  run = "cloche-metric complexity"
  max_complexity = 10
  results = [pass, fail]
}
```

Metrics wire into workflows exactly like test and lint steps:

```
workflow "develop" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "cargo test 2>&1"
    results = [success, fail]
  }

  step check-size {
    run = "cloche-metric code-length"
    max_file_lines = 500
    results = [pass, fail]
  }

  step check-functions {
    run = "cloche-metric function-size"
    max_function_lines = 60
    results = [pass, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = 3
    feedback = "true"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort

  test:success -> check-size
  test:success -> check-functions
  test:fail -> fix

  check-size:fail -> fix
  check-functions:fail -> fix
  collect all(check-size:pass, check-functions:pass) -> done

  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
```

### Diff Detection

`cloche-metric` determines which files changed by comparing the current working tree
against the base SHA. The base SHA is available via `clo get base_sha` (already stored
by the engine at run start).

```
git diff --name-only --diff-filter=ACMR <base-sha> HEAD
```

The `--diff-filter=ACMR` flag includes Added, Copied, Modified, and Renamed files,
excluding Deleted files (which have no content to analyze).

If the base SHA is unavailable (e.g., during local development), `cloche-metric` falls
back to analyzing all tracked files with a warning on stderr.

### Findings Output Format

Metric findings are written as plain text with one finding per line, suitable for
inclusion in agent prompts via the feedback mechanism. The format:

```
<file>:<line>: <message>

```

Examples:

```
src/server.go:0: file is 723 lines (max 500)
src/handler.go:45: function HandleRequest is 82 lines (max 50)
src/parser.go:120: function parseExpression has cyclomatic complexity 15 (max 10)
```

Line 0 indicates a file-level finding. The output is written to both:

1. **stdout** — captured as `.cloche/output/<step-name>.log`, picked up by the
   `readFeedback()` mechanism when a downstream fix step has `feedback = "true"`.
2. **KV store** — at key `metric_<step-name>` via `clo set metric_<step-name> -` (stdin),
   accessible to any step via `clo get`.

### LSP Integration

`cloche-metric` includes an LSP client for language-aware metrics (function-size,
complexity). The client:

1. Discovers the appropriate language server based on file extension. A built-in
   mapping covers common languages (e.g., `.go` → `gopls`, `.py` → `pylsp`,
   `.rs` → `rust-analyzer`, `.ts`/`.js` → `typescript-language-server`).
2. Starts the language server as a subprocess.
3. Sends `initialize`, `textDocument/didOpen`, and `textDocument/documentSymbol`
   requests for each changed file.
4. Extracts function/method symbols with their start/end line ranges.
5. Shuts down the server gracefully.

If no language server is installed for a given file's language, `cloche-metric` skips
that file with a warning on stderr. This means language support is determined by what's
available in the container image — customers add language servers to their Dockerfile
to enable analysis for their stack.

The LSP client lives in a package under `cloche-metric` (e.g.,
`cmd/cloche-metric/lsp/`) and is not exported as a library.

### Custom Metrics

Customers write their own metric scripts following the same conventions:

1. Read thresholds from the KV store (via `clo get`).
2. Determine changed files (via `git diff` against base SHA from `clo get base_sha`).
3. Analyze files, write findings to stdout in `file:line: message` format.
4. Store findings: `clo set metric_<step-name> - < findings.txt`
5. Emit `CLOCHE_RESULT:pass` or `CLOCHE_RESULT:fail`.

A custom metric is just a script step:

```
step check-todo {
  run = "bash .cloche/scripts/check-todos.sh"
  max_todos = 5
  results = [pass, fail]
}
```

The `max_todos` config key is written to the KV store by the engine before execution,
so the script reads it with `clo get max_todos`.

No registration or plugin system is required.

### Threshold Configuration via KV Store

Metric thresholds are specified as step config keys in the DSL. Before executing a
metric step, the engine writes the step's config keys to the KV store so that
`cloche-metric` can read them via `clo get`. This uses the same KV mechanism that
already crosses the host/container barrier.

For a step configured as:

```
step check-size {
  run = "cloche-metric code-length"
  max_file_lines = 500
  results = [pass, fail]
}
```

The engine writes `max_file_lines` → `500` to the KV store before the step runs.
`cloche-metric` then reads:

```
threshold=$(clo get max_file_lines)
```

If the key is absent, the built-in default applies.

The DSL parser's `knownStepConfigKeys` set in `internal/domain/workflow.go` is extended
to include the metric threshold keys (`max_file_lines`, `max_function_lines`,
`max_complexity`) so the parser does not reject them.

This approach has two advantages over environment variable injection:

1. **Host/container portability.** KV keys are accessible from both host workflows and
   container workflows without separate injection paths.
2. **Runtime overrides.** An earlier step can `clo set max_file_lines 300` to tighten
   thresholds dynamically based on project context.

### Container Image Changes

The base container image (built from `.cloche/Dockerfile` or the default Cloche base
image) includes `cloche-metric` on `$PATH` alongside `cloche-agent` and `clo`. For
language-aware metrics, customers add language servers to their project Dockerfile:

```dockerfile
# Enable Go metrics
RUN go install golang.org/x/tools/gopls@latest

# Enable Python metrics
RUN pip install python-lsp-server

# Enable Rust metrics
RUN rustup component add rust-analyzer
```

The `code-length` metric requires no language server and works out of the box.

### Error Handling

- **No changed files**: `cloche-metric` emits `CLOCHE_RESULT:pass` with no findings.
  Nothing to check means nothing failed.
- **Language server not found**: Files for that language are skipped with a stderr
  warning. If all changed files are skipped, the result is `pass` (no findings).
- **Language server crashes**: The file being analyzed is skipped with a stderr warning.
  Other files continue.
- **Base SHA unavailable**: Falls back to all tracked files with a stderr warning.
- **Invalid threshold config**: `cloche-metric` exits with an error message and
  `CLOCHE_RESULT:fail`. The step output explains the misconfiguration.

## Alternatives Considered

**New `metric` step type.** Rejected because it adds parser complexity, a new adapter,
and changes to the engine — all for something that works naturally as a script step.
Metrics have no execution semantics that differ from scripts: they run a command, produce
output, and report a result.

**Tree-sitter for AST parsing.** Rejected in favor of LSP. Language servers are
maintained by language communities, support the full grammar of each language, and are
already familiar tooling. Tree-sitter would require embedding grammars and maintaining
bindings. LSP delegates language support to the container image, making it the customer's
choice.

**Metric history and trend tracking.** Deferred. Per-run pass/fail is sufficient for the
initial release. History can be added later by having the daemon record metric findings
from the KV store into a dedicated table, but this adds storage and UI concerns that are
out of scope.

**Separate metrics config file (`.cloche/metrics.toml`).** Rejected because thresholds
in the DSL step config are simpler and more visible. A config file adds indirection
without clear benefit for the initial set of metrics.

**Warn result level.** The built-in metrics use only `pass`/`fail`. Customers who want
a `warn` result can define it in their custom metric scripts — the existing wiring
system handles arbitrary result names. This avoids complicating the built-in metrics
with three-tier severity before there's demand for it.
