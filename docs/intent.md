# Intent Continuity

Cloche runs are stateless with respect to *intent*. Each run's agent sees its task
prompt and the workflow's prompt templates, but not the accumulated constraints and
decisions that govern the project — "never bump the major version", "containers are
seeded from a clean snapshot, never the live working tree", "always review an agent's
changes before merging". Those live scattered across `CLAUDE.md`, design docs, old run
transcripts, task descriptions, and commit messages, and an agent that doesn't
rediscover them re-litigates settled decisions or violates standing constraints.

Intent continuity extracts those durable statements once, stores them as plain files
under `.cloche/intent/`, and injects the ones relevant to a given step automatically.
For the full design rationale (goals, non-goals, the retrieval spike results) see
[`docs/plans/2026-09-13-intent-continuity-design.md`](plans/2026-09-13-intent-continuity-design.md).

**The feature is dormant until a scan runs.** A project with no `.cloche/intent/`
directory gets no injection, no warnings, and no behavior change — byte-identical
prompts to a project that never adopted the feature. By default a scan runs
automatically after the first completed `main` orchestration task (see "Incremental
scans on task completion" below); set `intent.scan_after_tasks = false` to keep it
fully manual instead.

## Concepts

| Concept | Description |
|---------|-------------|
| **Requirement** | One durable statement of intent: a constraint ("never X") or a decision ("we use Y because Z"). Has provenance, scope, status, and confidence. |
| **Domain map** | The project's major architectural systems, each with a name, description, and path globs. Discovered by a scan; user-editable. |
| **Provenance** | Where a requirement came from: source kind (`doc`, `transcript`, `prompt`, `commit`, `user`) plus a locator (file+heading, run ID, task ID, commit SHA). |
| **Scope** | What a requirement applies to: `project` (always injected) or one or more `domain`s, optionally narrowed by path glob or language. |
| **Status** | `active`, `disabled` (a user turned it off), or `superseded` (newer evidence replaced it; points at its successor). Only `active` requirements are ever injected. |
| **Retrieval hints** | Short phrases describing situations where a requirement applies, in the words someone would use when they hit the problem — embedded for semantic search but never shown to an agent. |

## Storage: `.cloche/intent/`

Files are the source of truth — checked into git, reviewable in PRs, editable in any
editor. The daemon parses them on demand with an mtime-keyed cache; there's no database
copy to drift out of sync.

```
.cloche/intent/
├── domains.yaml            # discovered domain map
├── requirements/
│   ├── req-a3f8.md         # one file per requirement
│   └── req-b91c.md
└── scan-state.yaml         # extraction cursors (last scanned commit, docs, runs)
```

A sibling `.cloche/intent-index/` directory holds the embedding index — a derived,
gitignored artifact, never committed. It self-heals: any requirement whose content
hash or embedding-model ID doesn't match is re-embedded on load.

### Requirement file format

Markdown with YAML frontmatter. The body is the requirement's statement plus optional
rationale — the part an agent reads; the frontmatter is the part the machinery reads.

```markdown
---
id: req-a3f8
status: active            # active | disabled | superseded
superseded_by: ""         # req id, when status == superseded
scope:
  level: domain           # project | domain
  domains: [versioning]
  paths: []                # optional narrowing globs
  languages: []             # optional, e.g. [go]
hints:                     # retrieval-only phrasings; embedded, never injected
  - "when to bump minor vs build number"
  - "is this change a breaking change"
confidence: high           # high | medium | low
user_edited: false         # true once a human touches statement/scope
provenance:
  kind: doc                # doc | transcript | prompt | commit | user
  ref: "CLAUDE.md#versioning"
  extracted_at: 2026-09-13T10:00:00Z
  extracted_by: intent-scan
created: 2026-09-13T10:00:00Z
updated: 2026-09-13T10:00:00Z
---

Never bump the major version unless explicitly told to. Minor bumps are for new major
features and backward-incompatible changes; everything else bumps the build number.

**Why:** Major releases are batched manually at the maintainer's direction.
```

IDs are `req-` plus 4 hex chars, collision-checked at creation and stable across edits —
renaming the statement never changes the file name. One file per requirement keeps PR
diffs scoped to a single requirement and gives each one its own `git log --follow`
history.

### Domain map format

```yaml
# .cloche/intent/domains.yaml
version: 1
domains:
  - name: versioning
    description: Version string management, release process, changelog.
    paths: ["internal/version/**", "docs/plans/*release*"]
```

Edit this file directly, or through `cloche intent`/the dashboard. A scan proposes
additions and description touch-ups but never removes or rewrites a domain a human
marked `user_edited: true`.

## Extraction: `cloche intent scan`

Extraction is an agent job, run through Cloche itself. `intent-scan` is a **built-in
host workflow** — its graph, agent prompts, and script steps are compiled into the
`cloched`/`cloche` binaries (see [`docs/workflows.md`](workflows.md#built-in-workflows)),
so it's available in any project with no setup. `cloche intent scan` (alias for
`cloche run intent-scan`) dispatches it; the workflow has six steps:

1. **discover-domains** — surveys the repo layout and proposes/updates
   `domains.yaml`. Full survey on first scan; incremental proposals afterwards,
   respecting `user_edited` domains.
2. **collect-sources** — deterministic, no LLM. Gathers material changed since the
   last scan: doc files (`CLAUDE.md`, `README*`, `docs/**/*.md`,
   `.cloche/prompts/**` by default), completed-run transcripts and task prompts not
   yet mined, and `git log` since the last scanned commit (noise-filtered the same
   way the changelog workflow is). Emits `none` — a no-op — when every source is
   already at its cursor, so a quiet re-scan does nothing.
3. **extract** — reads the collected material plus `domains.yaml` and writes
   candidate requirements: statement, rationale, proposed scope, confidence,
   provenance, and 2–5 retrieval hints per candidate. Only durable, prescriptive
   intent is extracted — not task-specific instructions, facts derivable from
   reading the code, or transient state.
4. **reconcile** — compares candidates against existing requirements and, for each,
   decides **create** (novel), **merge** (already tracked, no-op), **supersede**
   (contradicts an active requirement with newer evidence — a new file is created,
   the old one flipped to `status: superseded`), or **drop** (not durable intent
   after all).
5. **apply-reconcile** — a post-step validation script that writes the reconcile
   step's decisions to `.cloche/intent/` and enforces the hard rules regardless of
   what the agent proposed:
   - A `disabled` requirement is never re-enabled or superseded away.
   - A `user_edited` requirement's statement and scope are never rewritten in
     place — only `supersede` may touch it, and only its status.
   - Nothing is ever deleted; `superseded` is the terminal state.
6. **commit** — stages and commits any changes under `.cloche/intent/` (and only
   that path — never `git add -A` / `git commit -a`), so a scan never leaves the
   main worktree dirty for a human or a later container-authored merge step to
   clean up. No-ops cleanly (no empty commit) when the scan found nothing new. The
   commit message reports what `apply-reconcile` actually did (counts of created,
   superseded, merged, and dropped candidates) plus whether `domains.yaml`
   changed. Retries a few times on index-lock contention (e.g. a concurrent scan
   or merge step) before failing.

```
cloche intent scan            # incremental: only material since the last scan
cloche intent scan --full     # currently has no effect beyond the incremental run — see below
```

A project can still override the built-in by defining its own `intent-scan` workflow
in a `.cloche/*.cloche` file (e.g. to swap the agent or tune timeouts) — project-defined
workflows always take precedence over built-ins of the same name. `collect-sources` and
`apply-reconcile` (`cloche intent collect-sources` / `cloche intent apply-reconcile`)
are the plumbing subcommands the workflow's script steps invoke — not normally run by
hand.

**What the first scan covers.** `collect-sources`' cursors (`scan-state.yaml`) start
empty, so the very first scan of a project already walks everything it looks at: every
doc matching the configured globs, the full `git log`, and every existing
`.cloche/runs/` directory — there's no separate "shallow first pass" to worry about.
The catch is *where* it looks: `collect-sources` roots every source — doc globs,
`git log`, and `.cloche/runs/`/`.cloche/logs/` — strictly at the project directory
itself (see `internal/intent/scan/collect.go`). It does not read `.cloche/config.toml`'s
`[[repositories]]` entries at all.

**Multi-repo projects.** In a project laid out as a thin orchestration wrapper with one
or more repositories checked out under it (e.g. `repos/<name>/`, declared via
`[[repositories]]` — see `docs/workflows.md`), the wrapper's own `.git` history is
typically tiny (often just one commit per completed task), while the real project
history, docs, and design decisions live inside `repos/<name>/.git` and
`repos/<name>/docs/`. Because `collect-sources` never descends into configured
repositories, a scan on a project like this — first scan or incremental — only ever
mines the wrapper's own docs, commits, and run transcripts. In practice this shows up
as "the scan only found the feature that just landed": the wrapper's one recent commit
and run *is* the entirety of what got collected, no matter how long the wrapped
repositories' real history is. Today, working around this means keeping durable docs
(`CLAUDE.md`, design docs) at the wrapper root, or running `cloche intent scan` from
inside the repository you want mined directly (its own `.cloche/` state is separate
from the wrapper's). Making `collect-sources` iterate every configured repository is
tracked as a follow-up; it is not yet implemented.

**`--full` does not currently reset collection cursors.** `cloche intent scan --full`
passes `--full` through as the run's task prompt, but no step reads it:
`collect-sources` ignores the run prompt entirely and only ever mines material since
`scan-state.yaml`'s cursors, and `discover-domains` decides whether to do a full domain
survey purely by whether `domains.yaml` already exists, independent of this flag. So
`--full` has no effect on how much material `collect-sources` collects — there is
currently no supported way to force a full re-collection of a project's docs/commits/
runs after the first scan has already advanced past them (short of deleting
`.cloche/intent/scan-state.yaml`, which also discards the doc-hash and scanned-run
cursors). This is tracked as a follow-up.

**Incremental scans on task completion.** By default (`intent.scan_after_tasks = true`),
an incremental scan is enqueued automatically after each completed `main` orchestration
run, covering just that task's prompt, transcript, and commits. Set
`intent.scan_after_tasks = false` in `config.toml` to opt out. Incremental scans are
cheap: `collect-sources`' cursors mean there's usually very little new material to mine,
and a project is never scanned twice in parallel — if a scan is already queued or
running, a new trigger is skipped.

## Injection

When assembling a prompt for an agent step, the daemon selects requirements in three
stages:

1. **Status** — only `active` requirements are visible; `disabled` and `superseded`
   ones never are.
2. **Deterministic scope match** — `level: project` requirements are always included.
   `level: domain` requirements are included when a requirement's domain matches the
   step's domain context: domains whose `paths` overlap the workflow's declared
   `repos`, or an explicit `domains = [...]` step/workflow config key.
3. **Semantic retrieval** — the task description and step prompt are embedded and
   scored by cosine similarity against every active requirement's embedding
   (statement + rationale + hints). Requirements above a per-model similarity floor
   join the selection ranked by score, regardless of whether deterministic scoping
   caught them — this is what lets an unrelated-sounding task still surface a
   relevant requirement with no keyword overlap.

Selected requirements are ordered project-level first, then by similarity score
(deterministic-only matches rank by confidence, then recency), and rendered as:

```
## Standing project requirements

These are established constraints and decisions for this project. Follow them unless
the task explicitly overrides one. Requirement IDs like [req-a3f8] are for reference;
one you believe is wrong or outdated should be flagged in your output, not silently
ignored.

- [req-a3f8] Never bump the major version unless explicitly told to. ...
- [req-b91c] (container-runtime) Containers must be seeded from a clean per-run ...
```

A token budget (`intent.token_budget` in `config.toml`, default ~2000 tokens) truncates
from the bottom of the ranked list with a `(N more requirements omitted; run cloche
intent list)` marker.

### Where the block goes

- If the step's resolved prompt contains the `{{ $intent }}` template variable, the
  block is rendered there.
- Otherwise (the default case) it's **auto-prepended** to the prompt.

Both host and container agent steps receive the block — for container steps, the
daemon computes it and seeds it into the run's KV store as `intent` before dispatching
the step, so the in-container resolver picks it up through the normal KV tier. This
also means uncommitted edits to `.cloche/intent/` apply even though the container
itself is seeded from a clean git snapshot (see `docs/run-isolation/`).

Injected requirement IDs are recorded per step in the run KV as
`<workflow>:<step>:intent`, so a run's detail view can show exactly what an agent was
told.

### Opting out

```
workflow "develop" {
  intent_tracking = false
  ...
}
```

```
step implement {
  prompt          = file(".cloche/prompts/implement.md")
  intent_tracking = false
  results         = [success, fail]
}
```

A step or workflow opted out with `intent_tracking = false` also has its logs excluded
from `collect-sources`' transcript mining, so noise or sensitive step output never seeds
requirements.

`config.toml`'s `[intent]` table's `inject = "off"` disables injection project-wide.
See [`docs/workflows.md`](workflows.md#intent-injection) for the DSL-level details.

## Embedding backends

Semantic retrieval runs entirely local and daemon-side — no tokens, no network calls
at injection time. Adapters resolve in this order (first available wins):

1. **`onnx`** (default) — in-process inference via ONNX Runtime, built into `cloched`
   when built with the `onnx` tag (on by default in release builds). Default model:
   all-MiniLM-L6-v2 (384 dims, ~90 MB, ~10 ms/text on CPU). Model, tokenizer, and the
   `libonnxruntime` shared library are downloaded on first use to
   `~/.cache/cloche/models/`, checksum-pinned. EmbeddingGemma-300M is the documented
   upgrade, at ~7× the size, for projects that want the best retrieval quality.
2. **`ollama`** — calls a local Ollama server's `/api/embed` endpoint, for platforms
   where the `onnx`-tagged build is unavailable or for users already running Ollama.
3. **`keyword`** — token-overlap scoring, no model, always available. This is the
   degraded fallback, not an alternative — the project's retrieval spike measured
   embeddings roughly doubling recall@3 over keyword overlap.

`intent.embedder` in `config.toml` pins the adapter-chain resolution above to a specific
adapter (`"onnx"`, `"ollama"`, or `"keyword"`); empty uses the default chain order.
`intent.model` is not yet wired into resolution — the onnx adapter's model override is
still read from the `CLOCHE_INTENT_MODEL` environment variable instead.

When no embedder is available (unsupported platform, model download refused),
selection degrades gracefully to status + deterministic scoping plus keyword-overlap
retrieval, with a one-time warning in the daemon log. The feature never blocks a run
on embedding availability.

Only `cloched` links an embedder; `cloche`, `clo`, and `cloche-agent` stay pure-Go —
selection is exclusively the daemon's job.

## CLI: `cloche intent`

```
cloche intent list [--domain <name>] [--status <status>] [--project <dir>]
cloche intent show <id> [--project <dir>]
cloche intent edit <id> [--project <dir>]
cloche intent disable <id> [--project <dir>]
cloche intent enable <id> [--project <dir>]
cloche intent add "<statement>" [--domain <name>]... [--project <dir>]
cloche intent preview [--workflow <name>] [--step <name>] [--prompt "..."] [--project <dir>]
cloche intent scan [--full]
```

All mutations (`edit`, `disable`, `enable`, `add`) are plain file edits under
`.cloche/intent/` — visible in `git diff`, committed like any other change. Only
`scan` talks to the daemon. `preview` renders the exact block a given
workflow/step/prompt would receive — the same selection and formatting code the
daemon uses for injection — and is the first thing to reach for when selection
surprises you. See [`docs/USAGE.md`](USAGE.md#cloche-intent) for the full flag and
subcommand reference.

## Web dashboard

The console shell's centre pane doesn't surface an Intent panel yet (it's a placeholder
pending the detail-pane ticket — see [`docs/web-dashboard.md`](web-dashboard.md)). The
backing HTTP API remains available for scripting or a future UI: requirements CRUD
(`GET`/`PATCH /api/projects/{name}/intent/requirements`), the domain map
(`GET`/`PUT /api/projects/{name}/intent/domains`), scan dispatch
(`POST /api/projects/{name}/intent/scan`), and source doc content
(`GET /api/projects/{name}/intent/doc`).

## Configuration reference

`config.toml`'s `[intent]` table:

| Key | Default | Description |
|-----|---------|--------------|
| `embedder` | _(unset)_ | Pins the adapter chain to `"onnx"`, `"ollama"`, or `"keyword"`; empty uses the default chain order (see "Embedding backends" above). |
| `model` | _(unset)_ | Reserved for overriding the `onnx` adapter's model; not yet wired in — use the `CLOCHE_INTENT_MODEL` env var today. |
| `token_budget` | `0` | Overrides the ~2000-token default selection budget. Zero means "use the default". |
| `inject` | _(unset)_ | `"off"` disables auto-prepending requirements to agent-step prompts project-wide. |
| `scan_after_tasks` | `true` | Enqueue an incremental `intent-scan` after each completed `main` orchestration run. |

## Adopting the feature on an existing project

`cloche init` never creates `.cloche/intent/` itself, and with `intent.scan_after_tasks`
left at its default of `true`, the first completed `main` orchestration task bootstraps
it automatically (domain discovery runs on that initial pass). Set
`intent.scan_after_tasks = false` in `config.toml` to keep the feature fully dormant
until you opt in by hand; the built-in `intent-scan` workflow (see "Extraction" above)
needs no setup, so adopting it manually is just:

```
cloche intent scan --full
cloche intent list
```

Review what got extracted (`cloche intent list`, `cloche intent show <id>`), edit or
disable anything wrong, and commit `.cloche/intent/`. From then on, injection is
automatic, and incremental scans keep it current as the project evolves.

The `--full` above is really just "run the alias now"; since `scan-state.yaml` doesn't
exist yet, this first invocation already mines everything `collect-sources` looks at —
see "What the first scan covers" above for what that includes and, for a multi-repo
project, what it doesn't.
