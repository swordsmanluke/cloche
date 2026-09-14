# Intent Continuity Implementation Plan

**Date:** 2026-09-13
**Design:** [`2026-09-13-intent-continuity-design.md`](2026-09-13-intent-continuity-design.md)
**Spike:** [`spikes/2026-09-13-intent-retrieval/`](spikes/2026-09-13-intent-retrieval/)

Nine slices, each independently landable and green. Ticket IDs are filled in below;
dependencies are encoded in bead. Slices 1–4 are pure library work with no
user-visible behavior change; the feature activates for users in slice 5 and stays
dormant for any project without a `.cloche/intent/` directory throughout.

Versioning: slices 1–8 are build bumps (feature is dormant/partial). Slice 9 ships
the feature and carries the `version:minor` label per the versioning policy.

---

## Slice 1 — `internal/intent` core package  (`cloche-56iz`)

**Scope:** types and file store, no daemon integration.

- `internal/intent/model.go` — `Requirement`, `Domain`, `Provenance`, `Scope`,
  status enum, `hints` field; markdown-with-YAML-frontmatter (de)serialization.
- `internal/intent/store.go` — load/save `.cloche/intent/` (requirements/, domains.yaml),
  mtime-keyed cache, `req-<4hex>` ID allocation with collision check.
- `internal/intent/scanstate.go` — `scan-state.yaml` cursor read/write.

**Acceptance:** round-trip tests for every field including hints and user_edited;
malformed frontmatter yields a per-file error, not a panic; a missing intent dir
loads as empty without error (the dormancy guarantee).

## Slice 2 — Embedder port, ollama/keyword adapters, index  (`cloche-7mjf`)

**Scope:** `internal/intent/embed/` port + the two cgo-free adapters, and the index.

- `embed.go` — `Embedder` interface (Embed/ModelID/Dimensions), adapter-chain
  resolution (`intent.embedder` config pin; default chain onnx→ollama→keyword with
  onnx absent until slice 3).
- `ollama.go` — local `/api/embed` adapter; detect availability by probing the port.
- `keyword.go` — token-overlap fallback, always available.
- `index.go` — flat-file index under `.cloche/intent-index/`: (id, content hash,
  model ID, vector); rebuild entries on hash/model mismatch; brute-force cosine
  top-k; query-embedding cache per run.

**Acceptance:** chain falls through cleanly when ollama is down; index self-heals
after an edit and after a model swap; spike corpus checked in as a regression
fixture with a minimum hit@3 bar, run only when a live embedder is present.

## Slice 3 — ONNX in-process adapter  (`cloche-63oy`)

**Scope:** the vendable default backend. Isolated because it carries the cgo/build
risk.

- `embed/onnx.go` behind build tag `onnx`; all-MiniLM-L6-v2 by default,
  `intent.model` override (EmbeddingGemma documented).
- First-use download of model + tokenizer + `libonnxruntime` to
  `~/.cache/cloche/models/`, checksum-pinned; clear daemon-log warning and chain
  fallback when the platform is unsupported or download is refused.
- Release CI: ship the `onnx` tag for linux/amd64 + darwin/arm64 first; tag-less
  builds keep working via the chain (decision recorded in the design doc's open
  questions).

**Acceptance:** `cloched` built without the tag compiles cgo-free and starts the
chain at ollama; with the tag, a cold start embeds the fixture corpus and matches
the ollama all-minilm adapter's rankings on the regression fixture.

## Slice 4 — Selection and block formatting  (`cloche-96wy`)

**Scope:** turning (task, step, config) into the injected block.

- `select.go` — status filter; deterministic scope match (project-level, domain
  paths × workflow `repos`, explicit `domains = [...]` step/workflow key); semantic
  top-k (k=5) with per-model floor from the adapter; merge + ordering per design;
  token budget with truncation marker (`intent.token_budget`, default ~2000).
- `format.go` — the `## Standing project requirements` block with `[req-id]`
  markers and the flag-don't-ignore instruction line.

**Acceptance:** selection matrix table-tests (status × scope × domain-context, with
a stub embedder); budget truncation; deterministic-only matches rank by
confidence→recency; degraded keyword mode produces the same shape.

## Slice 5 — Prompt injection wiring  (`cloche-27w7`)

**Scope:** the feature becomes real for agent steps. Daemon-side resolution only.

- `$intent` built-in registered in the prompt-templating resolver (host tier calls
  select directly; container tier reads the value the daemon seeds into run KV as
  `intent` before each `ExecuteStep`).
- Auto-prepend when the resolved prompt lacks `{{ $intent }}`; opt-outs
  (`intent = "off"` step/workflow key, `intent.inject` in config.toml); fully
  dormant with no intent dir.
- Record injected IDs per step in run KV (`<workflow>:<step>:intent`).

**Acceptance:** host and container agent steps both receive the block (container
via KV seeding — works with uncommitted requirement edits, which the clean-git
snapshot would hide); explicit placement suppresses prepend; no intent dir → no KV
seeding, no prompt change, byte-identical prompts to today.

## Slice 6 — `cloche intent` CLI  (`cloche-ihc4`)

**Scope:** `list` (--domain/--status filters), `show`, `edit` (sets user_edited),
`disable`/`enable`, `add` (provenance kind=user), `preview` (renders the exact
block for a given --workflow/--step/--prompt), `scan` (alias dispatching the
intent-scan workflow).

All mutations are file edits via the slice-1 store; gRPC only where the daemon owns
state (scan dispatch). **Acceptance:** golden-output tests; `preview` matches what
slice 5 injects for the same inputs.

## Slice 7 — `intent-scan` workflow  (`cloche-5h4h`)

**Scope:** extraction. Built-in host workflow (pattern: `changelog`), overridable
by defining `intent-scan` in the project.

- Steps per design: `discover-domains` (agent) → `collect-sources` (script,
  cursors, emits `none` when quiet) → `extract` (agent → candidates.json, writes
  statements + rationale + **retrieval hints** — the spike's biggest quality
  lever) → `reconcile` (agent: create/merge/supersede/drop).
- Post-reconcile validation script enforces the hard rules: never delete, never
  re-enable disabled, never rewrite user_edited (supersede only).
- Sources v1: docs globs, run transcripts (filtered), task prompts, git log range.
- `intent.scan_after_tasks` config: incremental scan enqueued after a `main` run.

**Acceptance:** golden-file test for collect-sources cursors; end-to-end fixture
project through a stubbed agent adapter covering create/merge/supersede and each
validation rule; re-running a quiet scan is a no-op (`none` path).

## Slice 8 — Dashboard Intent tab + HTTP API  (`cloche-gmdk`)

**Scope:** visibility and curation UI on the Project Detail page.

- API: `GET/PATCH /api/projects/{name}/intent/requirements`, `GET/PUT .../domains`,
  `POST .../scan`.
- Tab: requirements table (statement, scope chips, status toggle, confidence,
  provenance link into Run Detail / commit diff / doc), edit drawer (sets
  user_edited), domain map editor, Scan-now button + last-scan badge for
  new-since-scan review, superseded/disabled filter hidden by default.

**Acceptance:** PATCH round-trips through the file store (visible in `git diff`);
provenance links resolve to existing pages; tab absent when project has no intent
dir.

## Slice 9 — Docs, polish, release  (`cloche-0vqg`, `version:minor`)

**Scope:** `docs/intent.md` (user guide: concepts, file format, scan, injection,
CLI, dashboard, embedder config/backends), USAGE.md + workflows.md cross-links,
CLAUDE.md note, `cloche init` scaffold mention, changelog entry. Final
end-to-end pass on this repo itself: run `intent-scan`, review extracted
requirements, confirm injection on a real `develop` run.

**Acceptance:** the dogfood scan produces a reviewed `.cloche/intent/` for cloche
itself; feature documented; minor version bump handled in finalize per policy.

---

## Sequencing

```
1 core ──> 2 embed/index ──> 3 onnx
   │            │
   │            └────> 4 select/format ──> 5 injection ──┐
   │                        │                            │
   │                        ├──> 6 cli ──────────────────┤
   │                        └──> 8 dashboard ────────────┼──> 9 docs+release
   └──────> 7 intent-scan ───────────────────────────────┘
```

Slices 3, 6, 7, 8 are parallelizable once their parents land. The minor bump rides
slice 9 only.
