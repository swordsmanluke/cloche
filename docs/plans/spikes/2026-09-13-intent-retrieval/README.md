# Intent retrieval spike (2026-09-13)

Validates the retrieval design in
[`2026-09-13-intent-continuity-design.md`](../../2026-09-13-intent-continuity-design.md):
can local-embedding similarity select the requirements relevant to a task prompt,
and do LLM-written augmentations help?

## Setup

- `corpus.json` — 28 hand-labeled requirements drawn from this repo's real docs
  (CLAUDE.md, workflows.md, release process), 14 realistic task-prompt queries with
  relevance labels; several queries share no words with their relevant requirements.
- `main.go` — harness. Embeddings via local Ollama `/api/embed`; generations (query
  expansions, retrieval hints) via `/api/chat`. Run: `go run .`
  (`GEN_MODEL=<ollama model>` selects the generator tier; caches are per-model JSON
  files, written incrementally, delete to regenerate).
- Embedding models: `all-minilm` (= all-MiniLM-L6-v2, the proposed default),
  `nomic-embed-text` (with task prefixes), `embeddinggemma`.
- Generator tiers: `llama3.2:3b` (temp 0) and `bonsai-8b-16k` (1-bit reasoning model;
  default temp, `num_predict` 6000 — temp 0 makes it reason forever and return empty
  content). A bonsai-27b tier was abandoned: ~58 s/generation and it leaks
  chain-of-thought into answers.

## Results

`run-3b.txt` / `run-8b.txt` hold full output. Headline (MRR / hit@1 / recall@3):

| Configuration | MRR | hit@1 | recall@3 |
|---|---|---|---|
| keyword overlap baseline | 0.48 | 0.36 | 0.35 |
| all-minilm plain | 0.63 | 0.50 | 0.60 |
| embeddinggemma plain | 0.69 | 0.57 | 0.52 |
| embeddinggemma + hints (3B) | 0.76 | 0.64 | 0.67 |
| **embeddinggemma + hints (8B)** | **0.80** | **0.71** | **0.70** |

## Conclusions

1. **Embeddings clearly beat keywords**; retrieval hints (document-side "applies
   when" phrases written by an LLM at scan time) are the single biggest quality
   lever — 13/14 queries retrieved in top-3 at the best configuration, and hint
   quality scales with the writer model (production hints come from the Claude-class
   extract agent, above the 8B tested here).
2. **Query expansion (injection-time LLM rewrite of the task) is rejected**: it hurt
   or was inconsistent at every generator tier, adds per-run latency, and
   hints+expand was never better than hints alone.
3. Selection should be **top-k with a per-model score floor**, not an absolute
   threshold (score distributions vary wildly across embedding models).
4. Local reasoning-tier models (1-bit bonsai) are impractical generators for this
   pipeline: slow, prone to empty/refusal outputs, and chain-of-thought leaks into
   answers. Non-reasoning 3B-class models are the local floor; they already produce
   useful hints.
