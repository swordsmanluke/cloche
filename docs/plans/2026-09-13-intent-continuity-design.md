# Intent Continuity Design

**Date:** 2026-09-13
**Status:** Proposed

## Problem

Cloche runs are stateless with respect to *intent*. Each run's agent sees its task
prompt, the workflow's prompt templates, and whatever it discovers in the workspace —
but not the accumulated constraints and decisions that govern the project: "never bump
the major version", "the local adapter is for tests only", "container seeding is a
clean git snapshot". These live scattered across design docs, CLAUDE.md, old run
transcripts, task descriptions, and commit messages. An agent that doesn't rediscover
them re-litigates settled decisions or violates standing constraints, and the user pays
for the same correction repeatedly.

The [intent-continuity](https://github.com/Emmimal/intent-continuity) demonstration
frames this well: **retrieving related history isn't the same as knowing which of it is
still true**. Its pipeline is: extract intent records from historical interactions →
scope them against a domain map → retrieve candidates per task → *verify* (drop
superseded and out-of-scope records) → compile survivors into the agent's context. Its
acknowledged weakness is exactly the part we must do differently: extraction and the
domain dictionaries are hand-authored rules, not robust enough for arbitrary projects.

## Goals

1. **Extract** requirements from project sources — docs, run transcripts, task prompts,
   git history — using Cloche's own agent infrastructure (LLM extraction, not
   hand-authored rules).
2. **Scope** requirements by an automatically discovered domain map (the project's major
   architectural systems), so injection is targeted, not a firehose.
3. **Inject** applicable, still-true requirements into LLM-step prompts automatically,
   with deterministic filtering (status, scope) at injection time, augmented by
   **local-embedding semantic retrieval** so selection generalizes to arbitrary
   projects without hand-tuned keyword rules — this is what makes the feature
   vendable rather than a demo.
4. **Expose** requirements to the user — what was extracted, where it came from, what it
   applies to — with the ability to edit, disable, and delete, in both the web dashboard
   and the CLI.
5. Requirements are **versioned with the project** and survive daemon restarts, branch
   switches, and machine moves.

## Non-Goals (v1)

- Embeddings at *scan* time (candidate dedup/supersession pre-filtering, automatic
  domain assignment). v1 uses embeddings for injection-time retrieval only; the scan
  reuses the same `Embedder` port later (see Future Work).
- Cross-project requirement sharing.
- Mining sources outside the project (Slack, issue trackers beyond bead task text).
- Automatic *enforcement* (e.g. a validator step that checks the diff against
  requirements). Injection only; enforcement is a natural follow-up workflow.

## Concepts

| Concept | Description |
|---------|-------------|
| **Requirement** | One durable statement of intent: a constraint ("never X") or decision ("we use Y because Z"). Has provenance, scope, status, confidence. |
| **Domain map** | The project's major architectural systems/domains, each with a name, description, and path globs. Discovered by an agent scan; user-editable. |
| **Provenance** | Where a requirement came from: source kind (`doc`, `transcript`, `prompt`, `commit`, `user`) plus a locator (file+heading, run ID, task ID, commit SHA). |
| **Scope** | What a requirement applies to: `project` (always injected) or one or more domains. Optionally narrowed by language or path glob. |
| **Status** | `active`, `disabled` (user turned it off), `superseded` (newer evidence replaced it; points at successor). |
| **Verification** | The filter applied at injection time: only `active` requirements whose scope matches the step's context are injected. Deep verification ("is this still true of the codebase?") happens during scans, not per-step. |

## Storage: `.cloche/intent/`

Files are the source of truth — checked into git, reviewable in PRs, editable in any
editor. The daemon parses them on demand (with an mtime-keyed in-memory cache); there is
no database copy to drift out of sync.

```
.cloche/intent/
├── domains.yaml            # discovered domain map
├── requirements/
│   ├── req-a3f8.md         # one file per requirement
│   └── req-b91c.md
└── scan-state.yaml         # extraction cursors (last scanned commit, run IDs)
```

### Requirement file format

Markdown with YAML frontmatter. The body is the requirement statement plus optional
rationale — the part an agent reads; the frontmatter is the part the machinery reads.

```markdown
---
id: req-a3f8
status: active            # active | disabled | superseded
superseded_by: ""         # req id, when status == superseded
scope:
  level: domain           # project | domain
  domains: [versioning]
  paths: []               # optional narrowing globs
  languages: []           # optional, e.g. [go]
hints:                    # retrieval-only phrasings; embedded, never injected
  - "when to bump minor vs build number"
  - "is this change a breaking change"
confidence: high          # high | medium | low (extractor's judgment)
user_edited: false        # true once a human touches statement/scope
provenance:
  kind: doc               # doc | transcript | prompt | commit | user
  ref: "CLAUDE.md#versioning"
  extracted_at: 2026-09-13T10:00:00Z
  extracted_by: intent-scan/jifo-intent-scan
created: 2026-09-13T10:00:00Z
updated: 2026-09-13T10:00:00Z
---

Never bump the major version unless explicitly told to. Minor bumps are for new major
features and backward-incompatible changes; everything else bumps the build number.

**Why:** Major releases are batched manually at the maintainer's direction.
```

One file per requirement (rather than one big YAML) so PR diffs are per-requirement,
agents can edit without merge conflicts, and `git log --follow` gives each requirement
its own history for free.

**IDs** are `req-` plus 4 hex chars (collision-checked against existing files at
creation). Stable across edits; renaming the statement never changes the file name.

### Domain map format

```yaml
# .cloche/intent/domains.yaml
version: 1
domains:
  - name: workflow-dsl
    description: The .cloche workflow DSL — parser, validation, step types, wiring.
    paths: ["internal/dsl/**", "docs/workflows.md"]
  - name: versioning
    description: Version string management, release process, changelog.
    paths: ["internal/version/**", "docs/plans/*release*"]
  - name: container-runtime
    description: Docker adapter, container lifecycle, seeding, extraction.
    paths: ["internal/adapters/docker/**"]
```

Users edit this file directly (or via the dashboard). The scan proposes additions and
description updates but never removes a domain a user added by hand (tracked the same
way as requirements: a `user_edited` flag per domain).

### Scan state

`scan-state.yaml` records cursors so scans are incremental: `last_commit` (git history
cursor), `scanned_runs` (run IDs already mined, pruned to a bounded window),
`scanned_docs` (path → content hash). Committed with the project so a fresh clone
doesn't re-mine everything and produce duplicates.

## Extraction: the `intent-scan` workflow

Extraction is an agent job, run through Cloche itself — same pattern as the existing
`changelog` workflow. A built-in host workflow, overridable per-project by defining
`intent-scan` in the project's own `.cloche` files.

```
workflow "intent-scan" {
  host {}

  step discover-domains {
    prompt  = <built-in prompt>       # skipped when domains.yaml exists & --full not set
    results = [success, fail]
  }

  step collect-sources {
    run     = <built-in script>       # deterministic: gathers new material since cursors
    results = [success, none, fail]
  }

  step extract {
    prompt  = <built-in prompt>       # mine collected sources → candidate requirements
    results = [success, fail]
  }

  step reconcile {
    prompt  = <built-in prompt>       # dedup/supersede against existing requirements
    results = [success, fail]
  }

  discover-domains:success -> collect-sources
  collect-sources:success  -> extract
  collect-sources:none     -> done      # nothing new since last scan
  extract:success          -> reconcile
  reconcile:success        -> done
  // fails -> abort
}
```

Invoked as `cloche run intent-scan` (on demand) and optionally on a cadence (see
Triggers below).

### Step details

**discover-domains** — Agent scans the repo layout, key docs, and module structure and
writes/updates `domains.yaml`. Runs fully only on first scan or `--full`; afterwards it
proposes incremental updates (new top-level packages, new docs) and respects
`user_edited` domains.

**collect-sources** — Deterministic script, no LLM. Assembles the material to mine into
`$temp_file_dir`:

| Source | What's collected | Cursor |
|--------|------------------|--------|
| Docs | Changed files matching configured globs (default: `CLAUDE.md`, `README*`, `docs/**/*.md`, `.cloche/prompts/**`) | content hash per path |
| Run transcripts | Agent-step logs of completed runs not yet mined, filtered to user-authored and decision-bearing text | run IDs |
| Task prompts | `--prompt` texts and bead task descriptions of completed tasks | task IDs |
| Git history | `git log last_commit..HEAD` messages (excluding the same noise commits the changelog workflow filters) | commit SHA |

Emits `none` when every source is at its cursor.

**extract** — Agent reads collected sources plus `domains.yaml` and emits candidate
requirements as JSON to `$temp_file_dir/candidates.json`. The prompt instructs it to
extract only durable, prescriptive intent (constraints, decisions-with-rationale,
standing preferences) — not task-specific instructions, code facts derivable from the
repo, or transient state. Each candidate carries statement, rationale, proposed scope,
confidence, and exact provenance.

**reconcile** — Agent compares candidates against existing requirement files and, for
each candidate, does one of: **create** (novel), **merge** (duplicate — keep existing,
optionally add provenance), **supersede** (contradicts an existing requirement with
newer evidence — new file created, old file's status set to `superseded` with
`superseded_by`), or **drop** (not durable intent after all). Hard rules enforced by a
post-step validation script, not left to the LLM:

- A `disabled` requirement is never re-enabled or superseded-away; the candidate that
  duplicates it is dropped (the user said no).
- A `user_edited` requirement's statement and scope are never rewritten in place; the
  scan may only supersede it, leaving the user's text intact in history.
- Nothing is ever deleted by a scan; `superseded` is the terminal state.

This is the design's answer to the repo's verification insight: supersession is
computed once, at scan time, by an agent that sees both records and the evidence — and
injection-time filtering is then a cheap deterministic status check.

### Triggers

- **On demand:** `cloche run intent-scan` (and a dashboard button).
- **Post-task (recommended default, opt-in per project):** config key
  `intent.scan_after_tasks = true` — after a `main` orchestration run completes, the
  daemon enqueues an incremental scan covering just that task's prompt, transcript, and
  commits. Incremental scans are cheap because `collect-sources` cursors make them
  near-empty.
- The finalize/merge workflow of a project can also invoke it as a `workflow_name` step.

## Injection

### Selection (per step)

When assembling a prompt for an agent step, the resolver selects requirements:

1. `status == active` — everything else is invisible to agents.
2. **Deterministic scope match:**
   - `level: project` → always included.
   - `level: domain` → included when any of the requirement's domains matches the
     step's **domain context**: the union of (a) domains whose `paths` overlap the
     workflow's declared `repos`/paths, and (b) an optional explicit step or workflow
     config key `domains = ["versioning", ...]`.
3. **Semantic retrieval:** the task description and step prompt are embedded and
   scored by cosine similarity against every active requirement's embedding (see
   Embedding & Retrieval below). Requirements above the similarity threshold
   (`intent.similarity_threshold`, default tuned by the spike) join the selection,
   ranked by score, regardless of whether deterministic scoping caught them. This is
   the clause that makes selection work on an arbitrary project with no hand-tuned
   keywords — a requirement about "release tagging" surfaces for a task that says
   "cut a new version" even though no word overlaps.
4. Ordering: project-level first, then the union of deterministic and semantic
   matches by similarity score (deterministic-only matches score by confidence,
   high→low, then recency). A token budget (default ~2000 tokens, config
   `intent.token_budget`) truncates from the bottom with a `(N more requirements
   omitted; run cloche intent list)` marker.

When no embedder is available (unsupported platform, model download refused),
selection degrades gracefully to steps 1–2 plus a keyword-overlap fallback for step 3,
with a one-time warning in the daemon log. The feature never blocks a run on
embedding availability.

The selected set is formatted as a compact block:

```
## Standing project requirements

These are established constraints and decisions for this project. Follow them unless
the task explicitly overrides one. [req-a3f8] etc. are IDs; a requirement you believe
is wrong or outdated should be flagged in your output, not silently ignored.

- [req-a3f8] (versioning) Never bump the major version unless explicitly told to. ...
- [req-b91c] (container-runtime) The local adapter is for tests only; real runs go ...
```

### Mechanics

Two complementary paths, both resolved **daemon-side** (the daemon reads
`.cloche/intent/` from the host project dir — this sidesteps the container's
clean-git-snapshot seeding, so uncommitted requirement edits still apply):

1. **Template variable** `{{ $intent }}` — a new built-in in the prompt templating
   engine for explicit placement. For container steps, the daemon computes the block
   and seeds it into the run KV as `intent` before dispatching each step, so the
   in-container resolver finds it through the existing KV tier without new plumbing.
2. **Auto-prepend (default on)** — if an agent step's resolved prompt does not contain
   `{{ $intent }}`, the block is prepended. This is what *ensures* inclusion per the
   feature's goal. Opt-out: step or workflow config `intent = "off"`; project-wide
   default via `intent.inject = "off"` in `config.toml`. A project with no
   `.cloche/intent/` directory gets no injection and no warnings — the feature is
   fully dormant until the first scan.

Injected requirement IDs are recorded per step (in the run KV as
`<workflow>:<step>:intent`) so the run detail page can show exactly what an agent was
told.

## Embedding & Retrieval

Semantic retrieval is what generalizes selection beyond hand-tuned rules, so it is a
core v1 component, not an add-on. It runs entirely **local and daemon-side** — no
tokens, no network calls at injection time, no per-step latency an agent would notice.

### `Embedder` port

```go
// internal/intent/embed/embed.go
type Embedder interface {
    // Embed returns one unit-normalized vector per input text.
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    // ModelID identifies model+revision; index entries are invalidated when it changes.
    ModelID() string
    Dimensions() int
}
```

Adapters, in default-selection order (first available wins; `intent.embedder` in
`config.toml` pins one explicitly):

1. **`onnx`** (default) — in-process inference via ONNX Runtime Go bindings inside
   `cloched`. Default model: **all-MiniLM-L6-v2** (384 dims, ~90 MB ONNX, ~10 ms per
   text on CPU) — small enough to vendor-fetch invisibly, strong enough for
   short-text retrieval (validated by the spike below). Model + tokenizer files and
   the `libonnxruntime` shared library are downloaded on first use to
   `~/.cache/cloche/models/`, checksum-pinned in the release. `intent.model` swaps in
   a larger model (e.g. EmbeddingGemma-300M) for projects that want it.
2. **`ollama`** — calls a local Ollama server's `/api/embed` when one is running.
   Useful where the cgo build is unavailable and for users who already run Ollama.
3. **`keyword`** — the degraded fallback: token-overlap scoring, no model. Always
   available.

**Build isolation:** only `cloched` links ONNX Runtime, behind a build tag
(`-tags onnx`, on by default in release builds). `cloche`, `clo`, and `cloche-agent`
stay pure-Go/cgo-free — selection is exclusively the daemon's job, so the in-container
binaries never need an embedder. A `cloched` built without the tag simply starts the
adapter chain at `ollama`.

### Index

Embeddings are a **derived artifact, never committed**: `.cloche/runs/` sibling
`.cloche/intent-index/` (gitignored by the standard pattern, like `runs/`) holding one
flat file of `(requirement id, content hash, model ID, vector)` records. On load, the
daemon re-embeds any requirement whose content hash or model ID doesn't match —
so user edits, scan output, fresh clones, and model swaps all self-heal with no
migration step. Corpus size is hundreds of requirements at most; brute-force cosine
over in-memory vectors is microseconds, and no vector database is warranted. Query
embeddings (task descriptions) are cached per run.

### What gets embedded

Per requirement: statement + rationale body + retrieval hints, prefixed with its
domain names — one vector per requirement. Per query: the task description concatenated with the step's
resolved prompt template *name* and the workflow name (not the full prompt body, which
is dominated by boilerplate). The spike validates this composition choice.

### Spike results (2026-09-13)

Validated against a hand-labeled corpus of 28 requirements drawn from this repo's real
docs and 14 realistic task-prompt queries, several with zero lexical overlap with
their relevant requirements. Code, corpus, and raw output:
`docs/plans/spikes/2026-09-13-intent-retrieval/`. Embeddings served by local Ollama
(quality transfers to the ONNX in-process adapter — same models).

| Retriever | hit@1 | recall@3 | recall@5 | MRR |
|---|---|---|---|---|
| keyword overlap (baseline) | 0.36 | 0.35 | 0.54 | 0.48 |
| **all-MiniLM-L6-v2** (proposed default) | 0.50 | 0.60 | 0.63 | 0.63 |
| nomic-embed-text (with task prefixes) | 0.57 | 0.56 | 0.70 | 0.66 |
| EmbeddingGemma-300M | 0.57 | 0.52 | **0.83** | 0.69 |

Conclusions baked into this design:

- **Embeddings clearly beat keywords** — MiniLM cuts top-3 misses from 7/14 to 4/14
  and nearly doubles recall@3. The keyword fallback is a degraded mode, not an
  alternative.
- **MiniLM stays the default** (90 MB, ~10 ms/text); **EmbeddingGemma is the
  documented `intent.model` upgrade** — best overall quality (recall@5 0.83) and the
  cleanest score separation (precision 1.0 at cosine ≥ 0.50 on this corpus), at ~7×
  the size.
- **Selection is top-k with a floor, not a bare threshold:** k = 5 with a floor of
  ~0.35 (MiniLM) / ~0.40 (EmbeddingGemma). nomic's compressed score range (everything
  ≥ 0.45) shows why absolute thresholds don't transfer across models — the floor is a
  per-model constant owned by the adapter, not a user-facing knob.
- **Every model misses the same four queries** — ones needing an inference step
  (e.g. "let one step download packages from npmjs.org" → the network-allowlist
  requirement). Two mitigations are in this design: deterministic domain scoping
  catches such cases when the step declares domains or the paths overlap, and the
  extract agent writes **retrieval hints** (see below) so the requirement's embedding
  carries symptom phrasing, not just the rule's own wording.

**Augmentation follow-up (same day):** two LLM-written augmentations were measured
at two local generator tiers (llama3.2-3B; bonsai-8B 1-bit) — full data in the spike
directory:

- **Retrieval hints (adopted):** the biggest quality lever found.
  EmbeddingGemma + 8B-written hints reaches **MRR 0.80 / hit@1 0.71 / recall@3
  0.70**, retrieving 13 of 14 queries in the top 3 (including all four
  reasoning-gap misses but one). Hint quality scales with the writer model, and
  production hints come from the Claude-class extract agent — the local tiers are a
  floor, not the ceiling.
- **Query expansion (rejected):** rewriting the task prompt with a local LLM at
  injection time hurt or was inconsistent at every tier (3B: MRR 0.63→0.44 on
  MiniLM; 8B: gains on one metric paid for by recall@5 0.83→0.67 on EmbeddingGemma),
  never beat hints alone even in combination, and adds per-run generation latency.
  Not part of this design.

### Retrieval hints

Each requirement's frontmatter may carry a `hints:` list — short phrases describing
situations where the requirement applies, written by the extract agent at scan time
("agent can't see my file", "add an npm dependency in a step"). Hints are appended to
the embedded text (statement + rationale + hints) but never rendered into the
injected block. This is the cheap, inspectable answer to the reasoning-gap misses:
the LLM does the inferential work once at extraction, so retrieval stays a dot
product. Users see and can edit hints like any other field. Validated by the spike's
augmentation follow-up: hints are the largest single quality lever measured
(EmbeddingGemma MRR 0.69 → 0.80 with hints from even an 8B local writer).

## User Interface

### CLI: `cloche intent`

```
cloche intent list [--domain D] [--status S]   # table: id, status, scope, source, statement (truncated)
cloche intent show <id>                        # full statement, rationale, provenance, history hint
cloche intent edit <id>                        # opens $EDITOR on the file; sets user_edited
cloche intent disable <id> / enable <id>       # flips status
cloche intent add "statement" [--domain D]     # manual requirement, provenance kind=user
cloche intent scan [--full]                    # alias for cloche run intent-scan
cloche intent preview [--workflow W --step S --prompt "..."]
                                               # renders the exact block a step would receive
```

All mutations are file edits in `.cloche/intent/` — visible in `git diff`, committed by
the user like any other change. `preview` is the debugging tool that pays for itself
the first time selection surprises someone.

### Web dashboard: Intent panel

New tab on the Project Detail page (alongside Workflow DAG):

- **Requirements table** — statement, scope chips (domain names), status toggle,
  confidence, provenance link. Provenance links resolve to the right existing page:
  run transcripts → Run Detail, commits → inline diff (reusing the prompt-history
  viewer), docs → file content at the referenced heading.
- **Inline edit** — statement and scope editable in a drawer (same pattern as the DAG
  step drawer); saving writes the file and sets `user_edited`.
- **Domain map view** — the domains with their path globs, editable.
- **Scan controls** — "Scan now" button (dispatches `intent-scan`), last-scan time,
  and a badge for requirements created by the latest scan so new extractions are
  reviewable at a glance.
- **Superseded/disabled filter** — hidden by default, one click to audit history.

Backing HTTP API on the daemon: `GET/PATCH /api/projects/{name}/intent/requirements`,
`GET/PUT .../intent/domains`, `POST .../intent/scan`.

## Code Shape

```
internal/intent/
  model.go        # Requirement, Domain, Provenance types; frontmatter (de)serialization
  store.go        # load/save .cloche/intent/ files; mtime cache; ID allocation
  select.go       # selection: status filter, scope match, semantic merge, budget
  format.go       # render the injected block
  scanstate.go    # cursor read/write
  index.go        # embedding index: load/rebuild on hash or model mismatch, cosine top-k
  embed/
    embed.go      # Embedder port + adapter chain resolution
    onnx.go       # in-process ONNX adapter (build tag `onnx`); model/runtime fetch
    ollama.go     # local Ollama /api/embed adapter
    keyword.go    # token-overlap fallback
internal/host/    # seed `intent` KV before each container step; auto-prepend for host agent steps
internal/adapters/agents/prompt/
                  # `$intent` built-in registration (host tier resolves via intent.Select;
                  # container tier hits the KV-seeded value)
cmd/cloche/       # intent subcommand family
internal/<web>/   # dashboard tab + HTTP handlers
assets/ or embedded:
                  # built-in intent-scan workflow, prompts, collect-sources script
```

The `intent.Store` is a concrete file-backed implementation, not a new port — files
are the only planned backend, and the dashboard/CLI/injection all consume the same
package. If a DB index becomes necessary (embeddings), it arrives as a cache inside
this package, not a second source of truth.

## Testing

- `internal/intent`: round-trip parse/serialize; selection matrix (status × scope ×
  domain-context permutations); token-budget truncation; ID collision handling;
  reconcile validation rules (disabled never re-enabled, user_edited never rewritten).
- `internal/intent/embed` + `index.go`: adapter-chain fallback order; index rebuild on
  content-hash and model-ID mismatch; deterministic∪semantic merge and ordering with a
  stub embedder; the spike's labeled corpus checked in as a regression fixture with a
  minimum hit@3 bar (run only when a real embedder is available, `-tags onnx` or a
  live Ollama).
- Prompt adapter: `$intent` resolves in host and container tiers; auto-prepend fires
  exactly when `{{ $intent }}` is absent and config allows; dormant when no intent dir.
- `intent-scan`: golden-file test for `collect-sources` cursor behavior; an end-to-end
  fixture project exercising create/merge/supersede through a stubbed agent adapter.

## Versioning

New major feature → **minor** version bump, per the versioning policy. All behavior is
additive and dormant for projects without `.cloche/intent/`.

## Future Work

- **Embeddings at scan time:** reuse the `Embedder` port to pre-filter reconcile work —
  candidate-vs-existing similarity flags likely duplicates and supersession pairs
  before the reconcile agent rules on them, cutting its token cost and misses. Same
  index, same port; deferred only to keep v1 landable.
- **Automatic domain assignment:** embed package/file summaries to keep `domains.yaml`
  path globs fresh as the repo grows, and to propose scope domains for new
  requirements.
- **Enforcement workflow:** a `check-intent` step template that reviews a run's diff
  against injected requirements and emits violations as a wire result.
- **Requirement feedback loop:** when an agent flags an injected requirement as wrong
  (per the injected block's instruction), surface that flag in the dashboard as a
  review queue item.
- **Transcript mining depth:** v1 mines decision-bearing text heuristically; a
  dedicated "session comment" convention (`clo set intent_note "..."` from inside a
  run) would give agents and users an explicit channel to record intent mid-run.

## Open Questions

- **cgo in release builds:** linking ONNX Runtime into `cloched` complicates
  cross-compilation (per-platform `libonnxruntime` artifacts). Mitigations: build tag
  keeps dev builds pure-Go; the adapter chain means a tag-less build still works via
  Ollama/keyword. Decide during implementation whether release CI ships the `onnx`
  tag for all platforms or only linux/amd64 + darwin/arm64 initially.
- **Similarity threshold & query composition:** set from the spike's numbers; must be
  re-validated once real extracted (rather than hand-labeled) requirements exist.
- **Auto-prepend default:** this design says default-on (dormant without an intent
  dir). If that proves too surprising, flip to default-off with `cloche init`
  scaffolds opting in.
- **Transcript volume:** long runs produce huge logs; `collect-sources` needs a
  filtering heuristic (user messages, final summaries, lines matching
  decision-language patterns) to keep extract-step token cost bounded. Tune with real
  data.
- **Naming:** `intent` vs `requirements` for the directory, subcommand, and variable.
  This doc uses `intent` (shorter, matches the technique's name); the dashboard label
  can still read "Requirements".
