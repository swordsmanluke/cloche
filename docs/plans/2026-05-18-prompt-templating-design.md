# Prompt Templating Design

**Date:** 2026-05-18
**Status:** Proposed

## Problem

Prompt templates today have almost no substitution machinery. `internal/adapters/agents/prompt/prompt.go::assemblePrompt` does two raw `strings.ReplaceAll` calls on single-brace placeholders:

- `{task_description}` → user prompt text (from `.cloche/runs/<task-id>/prompt.txt`)
- `{previous_output}` → preceding step's captured stdout

That's it. Workflow authors who want to bring data into a prompt — file contents, KV-store values, computed strings, lookup results — must either inline the data into the prompt at workflow-author-time (impossible for runtime values), or write a separate `run = "..."` step that pre-stages the data and have the agent's `Read` tool pick it up. Both are clunky, and the second forces an LLM round-trip to discover values cloche already knows.

The KV store (`cloche get`/`set`, `clo get`/`set`) gives steps a deterministic way to share named values across a run, but those values aren't reachable from prompt text without instructing the agent to call `clo get` itself — paying LLM tokens to re-discover a value the host already has.

## Goals

1. Let workflow authors inject KV values, built-in run metadata, file contents, and shell-command output into prompts using one consistent syntax.
2. Substitution is deterministic and happens before the LLM is invoked — no agent round-trips to fetch known data.
3. Failures surface early: missing variables / files / non-zero shell exits fail the step before the agent runs, not in the middle of a turn.
4. Migration path for the existing single-brace placeholders; no big-bang break of in-tree workflows.

## Non-Goals

- Templating in `run = "..."` script bodies. Scripts already have a full shell; layering a second DSL on top creates `$$`/`{{}}` interaction puzzles for no real win. Scripts that need KV values call `clo get` directly.
- Conditionals, loops, or filters (Jinja / Handlebars territory). Defer until a concrete need shows up.
- Recursive templating of injected content. File contents and shell stdout are inserted literally — a `{{ ... }}` sequence appearing inside an included CSV is not re-evaluated.
- Restoring wire output mappings (`[VAR = output.field]`). That syntax was removed on 2026-04-14 (see `2026-03-09-wire-output-mapping-design.md`); the KV store replaces it.

## Design

### Syntax

A single `{{ ... }}` form with a one-character directive prefix:

| Form | Meaning |
|------|---------|
| `{{ $name }}` | Variable lookup: built-in name, falling back to KV store |
| `{{! cmd }}` | Run `cmd` via `sh -c`; substitute its stdout |
| `{{@ path }}` | Read file at `path`; substitute its contents |
| `$$` | Literal `$` — only meaningful inside `{{! ... }}` |

Whitespace immediately inside `{{ }}` is trimmed: `{{$x}}` and `{{ $x }}` are equivalent.

**Bare `$name` inside directive bodies.** `{{! }}` and `{{@ }}` bodies resolve bare `$name` references against the same built-in / KV tiers as the top-level `{{ $name }}` form. The `{{` / `}}` characters that happen to appear inside a directive body are **literal** — they are not nested directives:

```
{{@ $temp_file_dir/data.csv }}                            # opens $temp_file_dir's value + "/data.csv"
{{! echo "task is $$TASK from $run_id" }}                 # shell-side $$TASK, KV-side $run_id
{{! echo '{{ $foo }}' }}   # with KV foo=bar             # runs echo '{{ bar }}'; result "{{ bar }}"
```

The parser still depth-counts `{{` / `}}` so the outer directive terminates at its true closing pair even when the body contains balanced braces (the third example above). Output of shell commands and file contents is **not** re-templated.

**Escape for literal `{{`.** Not in v1. If it becomes a real problem we can add `\{\{` later; today no prompt in the repo uses literal double-braces.

### Variable Resolution

`{{ $name }}` looks up `name` in this order:

1. **Built-ins** (reserved namespace; shadow KV):
   - `$task_id`
   - `$run_id`
   - `$step_name`
   - `$workdir`
   - `$prev_output` — preceding step's captured stdout (same value the old `{previous_output}` got)
   - `$task_description` — user prompt text (same value the old `{task_description}` got)
2. **KV store** — host-context steps go through the daemon's KV directly; container-context steps go through the same `clo`-style gRPC client `clo` itself uses. Either way, both surfaces share the daemon's single KV store, so a value `cloche set` writes is readable from a container step's prompt and vice versa.

A name that resolves in neither tier → step fails before the agent runs.

Built-ins are reserved: a workflow author cannot override `$task_id` with `cloche set task_id ...` and have it apply to prompts. (KV writes still work for the user's own purposes; they're just shadowed at template-resolution time.)

### Shell Directive

`{{! cmd }}` executes `sh -c cmd`:

- **Working directory:** the step's `workDir` (same as `assemblePrompt`'s `workDir`).
- **Environment:** the same environment the agent process gets, including `Adapter.ExtraEnv`. KV values are not auto-exported as env vars — use `{{ $name }}` inline or pipe `clo get name` inside the command.
- **`$$`:** inside `{{! ... }}` only, `$$` is rewritten to `$` after bare-`$name` resolution but before handing the string to `sh -c`. So `{{! echo $$FOOBAR }}` runs `echo $FOOBAR` (shell expands `$FOOBAR`), while `{{! echo $task_id }}` runs `echo <task-id-value>`.
- **Capture:** stdout is captured and trimmed of one trailing `\n` (most commands emit a trailing newline; users who want it can `echo -n`). stderr is captured into the step log (via the StatusWriter if present), so a workflow author can see what went wrong, but stderr is **not** substituted into the prompt.
- **Timeout:** 30s default; non-configurable in v1. Long-running data prep belongs in a separate `run` step.
- **Non-zero exit:** step fails before the agent runs. Error message names the directive and the exit code.

### File Directive

`{{@ path }}` reads the file at `path`:

- **Path resolution:** workDir-relative unless absolute. No sandboxing in v1 — same trust model as `run = "..."` scripts (the workflow author controls both).
- **Content:** raw bytes, inserted as a UTF-8 string. No trimming, no normalization. Inserted content is **not** re-templated.
- **Size cap:** none in v1. The size of the resolved prompt is already bounded by the agent's input limit; we'll add a warning log if a resolved prompt exceeds N MB only if it actually trips someone up.
- **Missing file / read error:** step fails before the agent runs.

### Error Model

`assemblePrompt` returns an error whenever any directive can't resolve. The step's `Execute` propagates this — no LLM invocation, attempt counter still increments (so `max_attempts`/`give-up` keep working), step result is `fail`, error is written to the step log.

Error message format names the directive and the underlying cause, so they round-trip to `cloche logs <run-id> --step <name>`:

```
prompt template: {{@ data.csv }}: open data.csv: no such file or directory
prompt template: {{! curl -fsSL ... }}: exit status 22
prompt template: {{ $missing_thing }}: variable not defined (built-in or KV)
```

### Migration of Existing Placeholders

`{task_description}` and `{previous_output}` continue to work. After the new templating pass runs, a legacy pass does the existing two `strings.ReplaceAll`s. If either pattern was actually substituted (not just present in the source after templating), the adapter logs a deprecation warning through its `StatusWriter`:

```
WARN [step=draft] {task_description} is deprecated; use {{ $task_description }}
```

Logged once per step per pattern (not per occurrence) to avoid noise on prompts that use the placeholder multiple times. Plan to remove the legacy pass in a minor version once in-tree workflows are migrated.

In-tree migration: `repos/cloche/.cloche/prompts/` plus the wrapper's own prompt files will be updated to the `{{ $... }}` form in the same PR that adds the feature. The legacy pass exists for out-of-tree workflows we don't control.

## Code Shape

### New: `internal/adapters/agents/prompt/template.go`

Self-contained module exposing one function:

```go
type Resolver struct {
    Builtins map[string]string
    KV       KVReader            // injected; host or container variant
    WorkDir  string
    Timeout  time.Duration       // for {{! ... }}
}

func (r *Resolver) Resolve(ctx context.Context, src string) (string, error)
```

`KVReader` is a tiny port (one method, `Get(key string) (string, bool, error)`) so the resolver is testable with an in-memory map.

The resolver walks the source once, finds `{{` / `}}` boundaries (respecting nesting), classifies each match by prefix character (`$`, `!`, `@`, or none), and evaluates. Inner-first nesting falls out naturally from a recursive-descent style: when resolving the body of an outer directive, run the same substitution pass on its body string first.

### Modified: `internal/adapters/agents/prompt/prompt.go`

In `assemblePrompt`, after `resolveContent` returns the template body and before the existing `strings.ReplaceAll` calls:

```go
content, err := resolveContent(tmpl, workDir)
if err != nil { ... }

content, err = templater.Resolve(ctx, content)  // NEW
if err != nil {
    return "", fmt.Errorf("prompt template: %w", err)
}

// existing legacy pass, with deprecation warning
if strings.Contains(content, "{task_description}") {
    a.warnDeprecated(step.Name, "{task_description}", "{{ $task_description }}")
    content = strings.ReplaceAll(content, "{task_description}", userPrompt)
    userPrompt = ""
}
// ... same for {previous_output}
```

The `Adapter` gets two new fields:
- `KV KVReader` — wired by the host executor and the in-container agent
- a deprecation-warning helper that logs through `StatusWriter` once per (step, pattern)

`Adapter.New()` keeps a nil `KV` so unit tests that don't exercise KV lookups can skip wiring it; `Resolve` errors cleanly if a `{{ $non_builtin }}` lookup is attempted with no `KV` set.

### Wiring

Two `KVReader` adapters, sharing nothing but the interface:

- **Host** (`internal/host/...`): wraps the daemon's existing KV implementation directly. The host executor constructs the `prompt.Adapter` for host-side prompt steps; pass the reader in there.
- **Container** (`internal/agent/...`): wraps a gRPC call to the daemon (`clo get` already does this — reuse the same client path). The in-container `cloche-agent` constructs the adapter for container-side prompt steps.

Both share the same daemon-backed KV, so values written from either side are visible from either side.

### Tests

In `internal/adapters/agents/prompt/template_test.go`:

- Plain text passes through unchanged.
- Each directive resolves correctly: `{{ $name }}`, `{{! cmd }}`, `{{@ path }}`.
- Built-in shadows KV: a `task_id` KV write does not override the built-in.
- KV miss → error; built-in miss → error.
- Missing file → error; non-zero shell exit → error; shell timeout → error.
- Bare `$var` inside `{{@ ... }}` resolves before the file is read.
- Bare `$var` inside `{{! ... }}` resolves before `sh -c`; `{{` / `}}` chars in the body flow through to the shell verbatim.
- `$$` is preserved as `$` only inside `{{! ... }}`, left alone elsewhere.
- File contents and shell stdout are **not** re-templated even if they contain `{{ ... }}` sequences.
- Whitespace inside `{{ }}` is tolerated.

In `prompt_test.go`:

- Legacy `{task_description}` and `{previous_output}` continue to substitute.
- Each legacy substitution emits exactly one warning to `StatusWriter` per step.
- A prompt that uses both legacy and new syntax for the same value (e.g. `{task_description}` and `{{ $task_description }}`) ends up with the correct content and one warning.

## Open Questions

- **Built-in catalog growth.** Likely additions over time: `$workflow_name`, `$attempt_count`, `$git_branch`, `$container_id`. Hold these until asked for; the catalog should not become a dumping ground.
- **`clo expand <template>` helper.** A CLI command that resolves a template against current KV for offline debugging would pay for itself the first time a prompt mis-renders. Worth a follow-up ticket once the resolver lands.
- **Sandboxing `{{@ path }}`.** Today the workflow author controls everything in `.cloche/` so absolute paths are fine. If we ever support third-party prompt fragments, revisit.

## Versioning

Build-number bump. New syntax is purely additive; legacy placeholders keep working with a warning, so no existing workflow breaks.
