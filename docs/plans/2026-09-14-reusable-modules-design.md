# Reusable Modules: Typed Inputs/Outputs and Namespacing

**Date:** 2026-09-14
**Status:** Proposed

## Problem

Cloche workflows currently share logic by copy-paste and naming convention. Every
prompt lives flat in `.cloche/prompts/`, every script flat in `.cloche/scripts/`, and
the only thing preventing two unrelated pieces of logic from colliding is discipline —
this project's own `.cloche/scripts/` directory contains a dozen files prefixed
`vertical-*` purely to avoid clashing with `release-*`, `changelog-*`, and each other.
There is no way to package "the steps, prompts, and scripts that implement code
review" as one reusable thing and drop it into two different workflows.

Data flow between steps has the same informality. Steps pass information to each other
by writing into the untyped, project-wide KV store (`cloche set` / `clo get`) or by
string-templating prompts (`{{ $some_key }}`). Nothing declares that a step expects a
`strictness` value that must be one of `strict`/`normal`/`lenient`, or that a script
reads `$target_branch` — you find out by reading the script. The `step.code.output`
dotted-path syntax accepted by the parser today is vestigial: it is parsed but never
interpreted at runtime, because there is no actual typed data-flow layer underneath it.

This makes anything larger than a single project's ad hoc scripts hard to share.
Cross-project reuse and reuse within a single project's multiple workflows both need
(a) a packaging boundary that owns its own prompts/scripts and can't leak into anyone
else's namespace, and (b) a typed contract for what a reusable unit consumes and
produces, so composing units together is a checkable operation rather than a read
through their internals.

## Goals

1. A **module** is a self-contained directory: one or more steps (and the wiring
   between them), the prompts and scripts those steps use, and a manifest declaring
   typed inputs and typed outputs.
2. Modules are **composable**: a workflow can use a module one or more times, each use
   independently namespaced, without its steps/scripts/prompts colliding with the host
   workflow's own or with another module's.
3. Inputs and outputs are **typed** (`string`, `int`, `bool`, `enum(...)`), checked by
   `cloche validate` wherever a value is a literal, and self-documenting wherever it's
   wired dynamically from another step's output.
4. All of this rides the **existing execution engine unchanged**. Module resolution is
   a parse-time expansion into an ordinary `domain.Workflow` graph — `cloche-agent`, the
   host runner, and the daemon executor do not need to know modules exist.

## Non-Goals (v1)

- Remote/versioned module sources (git URLs, a registry, semver dependency
  resolution). v1 modules are local directories under `.cloche/modules/`, referenced by
  relative path. A `source` field pointing at a git ref is plausible future work once
  there's a second project wanting to consume the same module.
- Changing how values are stored at runtime. The KV store stays a flat string map;
  types are a parse-time contract between module authors and callers, not a new
  runtime representation.
- A package registry, module search, or `cloche module install` tooling.
- Sharing a module's *container* (image/Dockerfile) independently of the project's own
  — a module is steps/prompts/scripts, not infrastructure. (It can still declare which
  `repos` it needs, same as a workflow can today.)

## Concepts

| Concept | Description |
|---------|-------------|
| **Module** | A directory under `.cloche/modules/<name>/` containing a manifest (`module.cloche`), and its own `prompts/` and `scripts/` subdirectories. |
| **Manifest** | The `module.cloche` file: declares typed `input`s, typed `output`s, the module's internal steps and wiring, and its declared `results` (the pseudo-terminals a caller wires against). |
| **Instance / alias** | A named use of a module inside a workflow (`use "modules/x" as foo { ... }`). The alias is the namespace: `foo`. A module can be used more than once per workflow, each with a distinct alias. |
| **Qualified name** | `<alias>.<internal-name>` — how an instance's steps, KV outputs, and log files are named once expanded into the host workflow, guaranteeing no collision with the host's own names or another instance's. |
| **Module result** | One of the manifest's declared `results` (e.g. `approved`, `changes_requested`). Internal wires that would otherwise target `done`/`abort` target these instead; the caller wires the instance itself against these names exactly like wiring an ordinary step's results. |
| **Export** | A manifest-level mapping from a declared `output` name to the KV key inside the module that holds its value, e.g. `export feedback_path = step.write_feedback.output.feedback_path`. |
| **Expansion** | The parse-time process that inlines a module's steps/wiring into the caller's `domain.Workflow`, rewriting names to their qualified form and rewriting the caller's wires against the instance into wires against the module's actual terminal steps. |

## Module Layout on Disk

```
.cloche/modules/
└── code-review/
    ├── module.cloche          # manifest: inputs, outputs, steps, wiring, results
    ├── prompts/
    │   └── analyze.md
    └── scripts/
        └── write-feedback.sh
```

A module is nothing more than a directory with this shape. There is no build step,
compilation, or copy phase: because the whole project root is already copied into the
container at `/workspace` (see [Container Model](../plans/2026-02-20-cloche-system-design.md#container-model)),
a module's scripts and prompts are simply present on disk wherever they're declared —
`file()` and `run` paths inside a manifest are resolved **relative to the module's own
directory** by the loader at expansion time, not relative to the project root and not
relative to the caller. A module author never encodes an install path, and a module
can be copied to a different project (or into `.cloche/modules/` under a different
name) without editing its manifest.

Two modules never collide on disk because they never share a directory. This is the
whole answer to "scripts and prompts from different modules can't collide" — the
collision is structurally impossible, not policed by convention.

## Type System for Inputs/Outputs

Four types, matched to the shapes that actually show up in step config and KV values
today:

| Type | Literal form | Notes |
|------|-------------|-------|
| `string` | any quoted string | default type if unspecified would be an error — type is always required for `input`/`output` declarations, to force authors to think about it |
| `int` | integer literal | validated as parseable at manifest-parse and use-site time |
| `bool` | `true` / `false` | |
| `enum(a, b, c)` | one of the listed bare identifiers | closed set; validated against the literal list |

Declarations:

```
input strictness {
  type    = enum(strict, normal, lenient)
  default = "normal"
}

input target_branch {
  type     = string
  required = true
}

output verdict {
  type = enum(approved, changes_requested)
}
```

`required` and `default` are mutually exclusive; an input with neither is optional and
resolves to an empty string if unset (matching today's KV-get-of-unset-key behavior).

**What gets checked, and when.** A value supplied at a `use` site is either a literal
(quoted string / bare identifier / number) or a dynamic reference (`step.x.output.y`,
or another used module instance's output). Literals are checked against the declared
type by `cloche validate`, same pass that today checks wiring completeness and
container-id consistency. Dynamic references are checked for *shape* when the source
is itself a typed output (its declared type must match the sink's declared type);
when the source is an ordinary step's untemplated stdout, there's no type to check
against, and the reference passes through unvalidated — exactly as untyped as
`{{ $prev_output }}` is today. This is intentionally partial: it upgrades the common
case (module-to-module composition, literal config) without pretending steps at large
have suddenly become typed.

At runtime nothing changes: exported outputs land in the KV store as ordinary string
values under a qualified key. Types are a compile-time contract, not a new wire
format — this is what keeps the execution engine untouched (see
[Expansion Model](#expansion-model)).

## Module Manifest Syntax

`module.cloche` uses the same lexer/grammar primitives as `.cloche` workflow files —
blocks, `key = value`, `file()`, wiring arrows — with a `module` top-level block instead
of `workflow`, and one addition: internal wires may target a declared result name
instead of only `done`/`abort`.

```
module "code-review" {
  input target_branch {
    type     = string
    required = true
  }
  input strictness {
    type    = enum(strict, normal, lenient)
    default = "normal"
  }

  output verdict {
    type = enum(approved, changes_requested)
  }
  output feedback_path {
    type = string
  }

  results = [approved, changes_requested, fail]

  step analyze {
    prompt  = file("prompts/analyze.md")
    results = [pass, changes, error]
  }

  step write_feedback {
    run     = "scripts/write-feedback.sh"
    results = [success, error]
  }

  analyze:pass    -> write_feedback
  analyze:changes -> changes_requested
  analyze:error   -> fail
  write_feedback:success -> approved
  write_feedback:error   -> fail

  export verdict       = step.analyze.result
  export feedback_path = step.write_feedback.output.feedback_path
}
```

Key rules, mirroring the existing workflow validator (`docs/workflows.md#key-properties`):

- `results` at the module level plays the same role `done`/`abort` play in a workflow:
  the only valid terminal targets for internal wiring. A module's internal steps must
  never wire to `done`/`abort` directly — doing so is a parse error, because a module
  is never the thing that ends a run; only the workflow that eventually uses it does.
- Every declared result must be reachable from at least one internal wire (same
  orphan-check the parser already runs on workflows).
- Every declared `output` must have exactly one `export`.
- `input`/`output` names live in their own namespace from step names; a manifest with
  an input and a step of the same name is fine.
- `analyze:pass -> write_feedback` uses bare step references exactly like today's
  intra-workflow wiring — inside its own manifest, a module is just a normal-looking
  workflow body. All of the qualification described below happens only when the module
  is *used*, not when it's authored.

## Referencing Modules from Workflows

A workflow uses a module with a `use` block, giving it a local alias:

```
workflow "develop" {
  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  use "modules/code-review" as review {
    input target_branch = "main"
    input strictness    = "strict"
  }

  implement:success -> review
  implement:fail    -> abort

  review:approved            -> done
  review:changes_requested   -> implement
  review:fail                -> abort
}
```

`review` behaves exactly like a step name in every wire it appears in: `implement`
wires to it as a normal target, and it must be wired for every result the module
declares (`approved`, `changes_requested`, `fail`) — the same "all declared results
must be wired" rule the parser already enforces for ordinary steps.

Inputs are supplied as `input <name> = <value>` lines inside the `use` block. `<value>`
is either a literal (type-checked against the module's declaration) or a dynamic
reference (`step.implement.output`, or `other_instance.some_output` to chain two
module instances). Supplying an input the module doesn't declare, or omitting a
`required` one, is a `cloche validate` error, same class as an undeclared `repos` name
today.

A module's declared `output`s become readable after it completes, both from later
prompt templates (`{{ $review.feedback_path }}`) and from `cloche get review.feedback_path`
in scripts — the qualified name (`<alias>.<output>`) is just a KV key, so nothing new
is needed on the reading side.

Using the same module twice needs two aliases:

```
use "modules/code-review" as first_pass  { input target_branch = "main"; input strictness = "lenient" }
use "modules/code-review" as second_pass { input target_branch = "main"; input strictness = "strict"  }
```

Both instances expand from the same on-disk manifest but produce entirely disjoint
qualified names (`first_pass.analyze` vs. `second_pass.analyze`), so they never
interfere even though they run in the same workflow graph.

## Namespacing Scheme

Everything a module contributes to a workflow, once used, is qualified by its alias
with a `.` separator — consistent with the dotted-path convention the parser already
accepts (`step.code.output`) and with the existing `container.` config-key prefix:

| Thing | Unqualified (inside the manifest) | Qualified (after expansion) |
|-------|-----------------------------------|------------------------------|
| Step name | `analyze` | `review.analyze` |
| Wire | `analyze:pass -> write_feedback` | `review.analyze:pass -> review.write_feedback` |
| Log file | — | `step.review.analyze.log` |
| KV output | `feedback_path` | `review.feedback_path` |
| KV per-step result key | — | `<workflow>:review.analyze:result` |
| Prompt/script path | `prompts/analyze.md` (relative to module dir) | resolved to `.cloche/modules/code-review/prompts/analyze.md`, unaffected by alias |

Note that prompt/script *paths* are namespaced by directory (one per module,
independent of how many times it's used or under what alias), while everything that
lives in the shared *runtime namespace* (step names, KV keys, log files) is namespaced
by alias. These are deliberately different axes: two instances of the same module
share one set of files but must never share one set of step names.

**Nested modules.** A module may itself contain a `use` block. Expansion is recursive:
using a module `outer` (which internally uses `inner` as alias `inner`) under alias
`foo` produces qualified names like `foo.inner.some_step`. `cloche validate` detects
cycles (a module transitively using itself) the same way it already detects duplicate
workflow names across files — by building the reference graph before expanding.

## Expansion Model

Expansion happens once, during parsing/loading, before a `domain.Workflow` is handed
to anything that executes it. Concretely, for each `use` block the loader:

1. Parses the referenced `module.cloche` (cached per path — reused across multiple
   `use` sites in the same or different workflows).
2. Validates supplied inputs against the manifest's `input` declarations (types,
   required-ness, unknown-input rejection).
3. Clones the module's steps and wiring, renaming every step to
   `<alias>.<step>` and rewriting `run`/`prompt file()` paths to be rooted at the
   module's own directory.
4. Rewrites internal wires that target a declared module result (e.g.
   `analyze:changes -> changes_requested`) into a wire from the qualified step to
   whatever the *caller* wired the instance's result to (e.g. `implement` from the
   example above) — collapsing the pseudo-terminal away entirely.
5. Rewrites the caller's wire *into* the instance (`implement:success -> review`) to
   target the module's qualified entry step (`review.analyze`).
6. Inserts a synthetic step (or wires directly, where possible) that copies each
   `export`ed KV key into its qualified name in the caller's KV scope once the
   module's boundary is reached.
7. Merges the resulting steps/wiring into the host `domain.Workflow`.

After this pass, the `domain.Workflow` handed to `cloche validate`, the host runner,
and `cloche-agent` is indistinguishable from one a user wrote by hand with everything
inlined and manually prefixed — because that is exactly what it is. **No change is
required to `internal/domain`, the daemon executor, or the agent runtime.** All new
logic lives in `internal/dsl` (module manifest parsing + the expansion pass) and in
`cloche validate` (the new type/namespace checks). This mirrors how host workflow
dispatch already treats `workflow_name` steps as a black box the executor doesn't need
to understand internals of — modules go one step further and disappear before
execution even starts.

## Validation Rules (additions to `cloche validate`)

- Module manifest structural checks: every declared `result` reachable, no internal
  wire to `done`/`abort`, every `output` has exactly one `export`, no duplicate
  `input`/`output`/step names within one manifest.
- Use-site checks: every `required` input supplied, no unknown inputs, literal inputs
  type-check against the declaration, every declared result of the instance is wired
  by the caller (reusing the existing "all declared results wired" checker,
  parameterized over the instance's declared `results` instead of a step's).
- Cross-reference checks: `source` path exists and contains a `module.cloche`; no
  cyclic `use` chain among modules.
- Everything modules touch downstream of expansion (container-id consistency, orphan
  steps, entry-point existence) is checked for free, because expansion runs before
  those existing checks do.

## End-to-End Example

```
.cloche/
├── develop.cloche
└── modules/
    └── code-review/
        ├── module.cloche
        ├── prompts/analyze.md
        └── scripts/write-feedback.sh
```

`develop.cloche`:

```
workflow "develop" {
  container { image = "my-project:latest" }

  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  use "modules/code-review" as review {
    input target_branch = "main"
    input strictness    = "strict"
  }

  step merge {
    run     = "scripts/merge.sh"
    results = [success, fail]
  }

  implement:success -> review
  implement:fail    -> abort

  review:approved          -> merge
  review:changes_requested -> implement
  review:fail              -> abort

  merge:success -> done
  merge:fail    -> abort
}
```

After expansion, the effective graph the daemon executes has steps `implement`,
`review.analyze`, `review.write_feedback`, `merge` — with `review.analyze:changes`
wired straight to `implement`, and `review.write_feedback:success` wired straight to
`merge`, exactly as if a developer had inlined and manually prefixed the module by
hand, minus the chance of getting the prefixing wrong.

## Migration Path

Nothing breaks: workflows with no `use` blocks parse and execute exactly as before,
and the expansion pass is a no-op for them. Existing conventions like this project's
own `vertical-*`-prefixed scripts in `.cloche/scripts/` are the manual, error-prone
version of what modules formalize — they're a natural (but separate, opt-in)
candidate for extraction into a `vertical` module once this ships, not something this
change needs to touch.

## Open Questions / Future Work

- **Remote modules.** `source` currently only accepts a local path
  (`modules/<name>`). A `git::https://...` form, with pinned refs, is the obvious next
  step once a second project wants to consume the same module — deferred because it
  needs a fetch-and-cache story this design doesn't (vendoring, offline builds,
  `cloche validate` behavior with no network).
- **Typed step outputs beyond exports.** Right now the type system only reaches as far
  as a module's boundary; a plain step's output is exactly as untyped as it is today.
  Extending declared types to ordinary step-to-step wiring (not just module I/O) would
  make the partial type-checking described above total, but is a larger change to the
  core DSL and out of scope here.
- **Module-level `repos` declaration.** Workflows can already scope which repositories
  get materialized via `repos = [...]`; modules probably want the same, merged into
  the host workflow's own `repos` list at expansion time. Straightforward, deferred
  only because it wasn't asked for by this task's requirements.
