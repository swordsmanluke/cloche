# Wire Output Mapping

**Date:** 2026-03-09
**Status:** Proposed

## Problem

Host workflow steps produce output that downstream steps need as structured
input. Today the only mechanism is `CLOCHE_PREV_OUTPUT`, a file path to the
previous step's raw output. Steps that need specific fields must parse the
output themselves, and there's no way to express "extract field X from step A's
JSON output and inject it as env var Y into step B" in the DSL.

This came up immediately: `ready-tasks` outputs JSON from `bd ready --json`,
and `prepare-prompt` needs `CLOCHE_TASK_TITLE` and `CLOCHE_TASK_BODY` as env
vars. Without DSL-level mapping, every step needs bespoke shell glue.

## Design

### Syntax

Output mappings live on wires, not on step declarations. A wire describes data
flow between two steps, so the mapping belongs there:

```
step-a:success -> step-b [ ENV_VAR = output.field, OTHER = output.list[0].name ]
```

The general form:

```
FROM:RESULT -> TO [ KEY = EXPR, KEY = EXPR, ... ]
```

Where:
- `KEY` is the environment variable name injected into the target step
- `EXPR` is an output path expression (see below)

### Output Path Expressions

All expressions start with `output`, which refers to the source step's output.

| Expression | Meaning |
|---|---|
| `output` | Raw output (full string) |
| `output.key` | JSON object field access |
| `output[N]` | JSON array index (0-based) |
| `output.key[N]` | Chained: field then index |
| `output[N].key` | Chained: index then field |
| `output.a.b.c` | Deeply nested field access |
| `output.a[0].b[1].c` | Arbitrary nesting |

If the source step's output is valid JSON, path expressions navigate the parsed
structure. If the output is plain text, only bare `output` is valid — any `.key`
or `[N]` access is a runtime error.

The resolved value is always converted to a string for injection as an env var.
JSON objects and arrays are re-serialized as JSON strings. Scalars use their
natural string representation.

### Examples

```
workflow "main" {
  step get-tasks {
    run     = "bd ready --json --limit 1"
    results = [success, fail]
  }

  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  get-tasks:success -> prepare-prompt [
    CLOCHE_TASK_ID    = output[0].id,
    CLOCHE_TASK_TITLE = output[0].title,
    CLOCHE_TASK_BODY  = output[0].description
  ]
  get-tasks:fail       -> abort
  prepare-prompt:success -> develop
  develop:success        -> done
  develop:fail           -> abort
}
```

Multiple wires into the same step merge their mappings. If two wires map the
same key, that's a validation error (ambiguous).

### Interaction with CLOCHE_PREV_OUTPUT

`CLOCHE_PREV_OUTPUT` continues to work as before — it always points to the
previous step's raw output file. Output mappings are additive: they inject
extra env vars alongside the existing ones. A step can use both the mapped
vars and read the raw file if needed.

## Implementation

### Domain Changes

Add an `OutputMap` field to `Wire`:

```go
type Wire struct {
    From      string
    Result    string
    To        string
    OutputMap []OutputMapping  // new
}

type OutputMapping struct {
    EnvVar string     // e.g. "CLOCHE_TASK_ID"
    Path   OutputPath // parsed expression
}

type OutputPath struct {
    Segments []PathSegment
}

type PathSegment struct {
    Kind  SegmentKind // SegmentField or SegmentIndex
    Field string      // for SegmentField
    Index int         // for SegmentIndex
}
```

### Lexer Changes

The lexer already supports `[`, `]`, `.`, `=`, `,`, and identifiers. Integer
literals are needed for array indices — add `TokenInt`.

### Parser Changes

After parsing `FROM:RESULT -> TO`, check for an optional `[` token. If present,
parse a comma-separated list of `IDENT = output PATH` mappings until `]`.

The `output` keyword is a contextual identifier — it's only special inside
mapping brackets. No new reserved words needed.

```
parseWire():
    from  = expect(TokenIdent)
    _     = expect(TokenColon)
    result = expect(TokenIdent)
    _     = expect(TokenArrow)
    to    = expect(TokenIdent)
    if peek() == TokenLBracket:
        mappings = parseOutputMappings()
    return Wire{from, result, to, mappings}

parseOutputMappings():
    expect(TokenLBracket)
    mappings = []
    loop:
        key  = expect(TokenIdent)
        _    = expect(TokenEquals)
        path = parseOutputPath()
        mappings.append({key, path})
        if peek() != TokenComma: break
        expect(TokenComma)
    expect(TokenRBracket)
    return mappings

parseOutputPath():
    expect(TokenIdent, "output")  // must start with "output"
    segments = []
    loop:
        if peek() == TokenDot:
            expect(TokenDot)
            segments.append(FieldSegment{expect(TokenIdent)})
        else if peek() == TokenLBracket:
            expect(TokenLBracket)
            segments.append(IndexSegment{expect(TokenInt)})
            expect(TokenRBracket)
        else:
            break
    return OutputPath{segments}
```

### Validation Changes

In `Workflow.Validate()`:
- If two wires target the same step and both map the same env var key, error.
- Output path expressions are syntactically validated at parse time (no runtime
  path validation — the output isn't known until execution).

### Executor Changes

In `internal/host/executor.go`, when launching a step:

1. Find all wires that target this step.
2. For each wire with output mappings, read the source step's output file.
3. Try to parse the output as JSON. If it's valid JSON, evaluate each path
   expression against the parsed value. If it's not JSON and any path has
   segments, return an error.
4. Add resolved values to the step's env as `KEY=value`.

```go
func (e *Executor) resolveOutputMappings(step string, wires []domain.Wire) ([]string, error) {
    var env []string
    for _, w := range wires {
        if w.To != step || len(w.OutputMap) == 0 {
            continue
        }
        data, err := os.ReadFile(e.stepOutputPath(w.From))
        if err != nil {
            return nil, fmt.Errorf("reading output of %s: %w", w.From, err)
        }
        for _, m := range w.OutputMap {
            val, err := m.Path.Evaluate(data)
            if err != nil {
                return nil, fmt.Errorf("mapping %s for step %s: %w", m.EnvVar, step, err)
            }
            env = append(env, m.EnvVar+"="+val)
        }
    }
    return env, nil
}
```

### Path Evaluation

```go
func (p OutputPath) Evaluate(raw []byte) (string, error) {
    if len(p.Segments) == 0 {
        return string(raw), nil // bare "output"
    }

    var val any
    if err := json.Unmarshal(raw, &val); err != nil {
        return "", fmt.Errorf("output is not valid JSON")
    }

    for _, seg := range p.Segments {
        switch seg.Kind {
        case SegmentField:
            m, ok := val.(map[string]any)
            if !ok { return "", fmt.Errorf("expected object for .%s", seg.Field) }
            val, ok = m[seg.Field]
            if !ok { return "", fmt.Errorf("field %q not found", seg.Field) }
        case SegmentIndex:
            arr, ok := val.([]any)
            if !ok { return "", fmt.Errorf("expected array for [%d]", seg.Index) }
            if seg.Index < 0 || seg.Index >= len(arr) {
                return "", fmt.Errorf("index %d out of range (len %d)", seg.Index, len(arr))
            }
            val = arr[seg.Index]
        }
    }

    // Convert resolved value to string
    switch v := val.(type) {
    case string:
        return v, nil
    default:
        b, _ := json.Marshal(v)
        return string(b), nil
    }
}
```

## Task Breakdown

1. **Add `TokenInt` to lexer** — Recognize integer literals for array indices.
   Update lexer tests.

2. **Add `OutputMapping` and `OutputPath` types to domain** — New types in
   `internal/domain/workflow.go`. Add `OutputMap` field to `Wire`. Add
   `Evaluate` method to `OutputPath`. Unit test path evaluation.

3. **Extend parser to handle wire output mappings** — Parse `[ KEY = EXPR ]`
   after wire targets. Update parser tests with mapping syntax.

4. **Add validation for duplicate mappings** — Error if two wires into the
   same step map the same env var. Update validation tests.

5. **Wire mapping resolution in host executor** — Read source output, evaluate
   paths, inject env vars. Update executor tests.

6. **Update host.cloche to use mappings** — Replace the current
   `prepare-prompt` step to consume mapped vars instead of relying on
   pre-set env.
