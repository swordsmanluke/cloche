# Bonsai executor wrapper (E4)

Part of the [Intent A/B experiment](../../../docs/plans/2026-09-14-intent-ab-experiment-protocol.md).
An `agent_command` CLI that drives **bonsai-8b-16k** through host Ollama's
OpenAI-compatible endpoint, for use as the executor in both experiment arms
(wired in by E5's arm overlays — this directory is the wrapper on its own,
not an arm config).

## Why a minimal driver, not aider

The ticket's candidate was `aider --model ollama/bonsai-8b-16k` in
non-interactive mode. It was evaluated and rejected for this harness:

- Aider owns its own request loop (retries, prompt shaping, diff format) —
  getting it to apply the bonsai-specific rules below (temperature floor,
  generation cap, chain-of-thought stripping, exactly-one retry-on-empty)
  means fighting or monkey-patching aider's own request/response handling
  rather than just writing it.
- It's a pip package; this project's containers have no guaranteed
  build-time pip/network access, so depending on it would make the image
  build unreproducible in exactly the environments this experiment needs
  to run in.
- The task itself is deliberately trivial (scripted single/few-file edits)
  — a whole-file-rewrite edit loop covers it without diff parsing.

So this is the "else a minimal edit-loop driver" branch: a small,
dependency-free (stdlib only) Python CLI that owns the full request/response
cycle and can enforce the bonsai rules directly.

## Bonsai handling rules (from the retrieval spike)

Source: [`docs/plans/spikes/2026-09-13-intent-retrieval/README.md`](../../../docs/plans/spikes/2026-09-13-intent-retrieval/README.md).
bonsai-8b-16k is a 1-bit reasoning model with these failure modes, all
handled in `agent_command/`:

| Rule | Where |
|---|---|
| Temperature must never be 0 (temp 0 → infinite reasoning loop → empty content) | `ollama_client.clamp_temperature` — floors any `<= 0` value to `MIN_TEMPERATURE` |
| Generation length must be capped | `ollama_client.clamp_max_tokens` — defaults to 6000 (the spike's `num_predict`), hard ceiling 8192 regardless of caller input |
| Leaked chain-of-thought must be stripped before edits are applied | `cot.strip_chain_of_thought` — strips `<think>`/`<thinking>`/`<reasoning>` blocks, including an unterminated opening tag (the generation-cut-off case) |
| Retry once on empty content | `ollama_client.OllamaClient.complete_with_retry` — exactly one retry, judged on the post-CoT-stripped content |

## Contract with Cloche

This is an "unknown agent" `agent_command` (docs/built-in-agents.md
"Unknown Agents"): no default args, prompt on stdin, no required args.
`cli.py`'s module docstring has the full contract; in short:

- Full assembled prompt (task + injected intent block in arm B + the
  auto-appended `## Result Selection` block) arrives on stdin.
- `CLOCHE_RESULT_NONCE` arrives in the environment.
- Exactly one `CLOCHE_RESULT:<nonce>:<name>` line must reach stdout. The
  wrapper prefers to pass through whatever marker line the model itself
  produced (instructed to echo it verbatim in `cli.SYSTEM_PROMPT`); if the
  model forgets, `cli._fallback_marker` synthesizes one from whether edits
  were applied, using a result name the step actually declared (parsed back
  out of the `## Result Selection` block Cloche put in the prompt). Cloche's
  own engine also issues a recovery turn on a missing marker — this is a
  second line of defense, not the primary path.
- `-c`/`--resume` are accepted and ignored: the wrapper is stateless (no
  server-side session with bonsai), so a resume/recovery invocation is
  handled the same way as a fresh one.

Edit format: one fenced code block per changed file, headed by the file's
project-relative path, containing the file's complete new contents (whole-
file rewrite — no diff format for a weak model to get subtly wrong). See
`agent_command/edits.py`. Paths are resolved and checked against the
working directory before anything is written; a path that would escape it
(`../...`, an absolute path) fails the whole step rather than writing
partial edits anywhere.

## Layout

- `agent_command/` — the library: `ollama_client.py` (HTTP + bonsai rules),
  `cot.py` (chain-of-thought stripping), `edits.py` (parse/apply file
  blocks), `cli.py` (orchestration + Cloche's marker protocol).
- `bin/agent_command` — the executable Cloche actually invokes.
- `fixture/` — a trivial buggy Python file (`repo/calc.py`) and the task
  text describing the fix, used by both the automated tests and the
  `.cloche/` acceptance workflow below.
- `tests/` — unit tests per module plus `test_cli_integration.py`, which
  runs the full pipeline (including a real subprocess invocation of
  `bin/agent_command`) against a fake Ollama server
  (`tests/fake_ollama.py`). Run with:

  ```
  python3 -m unittest discover -s tests -v
  ```

- `.cloche/` — a standalone mini cloche project (separate from
  `experiments/intent-ab/seed/`) whose only workflow, `fixture_check`,
  exists to run the container-based acceptance check below.

## Configuration (environment variables)

All optional; defaults target a local Ollama on the standard port reached
through Docker's host gateway.

| Variable | Default |
|---|---|
| `BONSAI_BASE_URL` | `http://host.docker.internal:11434/v1` |
| `BONSAI_MODEL` | `bonsai-8b-16k` |
| `BONSAI_TEMPERATURE` | `0.8` (clamped up from anything `<= 0`) |
| `BONSAI_MAX_TOKENS` | `6000` (clamped to a ceiling of `8192`) |
| `BONSAI_TIMEOUT_SECONDS` | `120` |

## Running the acceptance check for real

The automated test suite proves the wrapper's logic against a fake server
(no real Ollama needed, so it runs in CI/sandboxes without GPU access). The
ticket's actual acceptance bar — *"from inside a cloche container, the
wrapper completes a trivial scripted edit task against a fixture repo three
times in a row"* — needs a real Ollama with `bonsai-8b-16k` pulled and is a
manual step, same as the arm runs themselves (E7/E8 in the protocol doc are
explicitly manual for the same reason: real-model runs need GPU access this
environment doesn't have).

1. On the host, serve bonsai: `ollama pull bonsai-8b-16k && ollama serve`
   (confirm it's listening on `11434`).
2. From this directory:
   ```
   cloche validate
   cloche run fixture_check
   ```
   Repeat 3 times (or loop it), resetting `fixture/repo/calc.py` to its
   buggy form between runs if a previous run already fixed it in place —
   the workflow doesn't reset it for you, since normally Cloche extracts
   each run to its own branch/worktree rather than mutating the checkout
   in place. Confirm each run reports `success` (`cloche status <run-id>`)
   and that the resulting `calc.py` has `return a + b`.

## Known limitations

- The edit format (whole-file rewrite in a path-headed fence) is
  deliberately simple for a weak model and untested against anything more
  than single/few small files; a larger task would need a real diff-apply
  loop.
- No test-running/self-correction loop — this is a single request/response
  turn per step invocation, matching the ticket's "minimal edit-loop
  driver" framing. Multi-turn correction (e.g. re-prompting on a failing
  `test` step) already exists at the workflow level (`fix-tests` in the
  seed's `develop.cloche`) and doesn't need to be duplicated here.
