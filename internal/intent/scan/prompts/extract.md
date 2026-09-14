# Extract Requirement Candidates

Mine the material collected by `collect-sources` for durable project intent —
constraints and decisions a future agent should know about — and write it as
structured candidates. This is the highest-leverage step in the scan: a
candidate's quality (and especially its retrieval hints) determines whether
the right requirement surfaces for the right future task.

## Inputs

```bash
SOURCES="$(cloche get intent_scan_sources_dir)"
TEMP="$(cloche get temp_file_dir)"
```

Under `$SOURCES`:

- `docs/<path>` — full content of each changed or new doc file, at its
  original project-relative path.
- `commits.txt` — `<sha>\t<subject>` per new commit (noise already filtered),
  and `diffs/<short-sha>.patch` for each.
- `runs/<id>/task_prompt.md` and `runs/<id>/transcript.log` — task prompts
  and step-output transcripts from runs not yet mined.
- `manifest.json` — a summary listing of everything above, if you want a
  quick index before reading files individually.

Also read `$CLOCHE_PROJECT_DIR/.cloche/intent/domains.yaml` for the domain
names available to scope candidates against.

## What counts as durable intent

Extract only:

- **Constraints** — "never X", "always Y", "X requires Y first".
- **Decisions with rationale** — "we use X because Y", including rejected
  alternatives when the source explains why.
- **Standing preferences** — conventions the project consistently follows
  that aren't obvious from reading the code alone.

Do **not** extract:

- Task-specific instructions that only make sense for one piece of work.
- Facts trivially derivable from reading the current code (e.g. "the Store
  type has a ListRequirements method").
- Transient state ("PR #123 is currently blocked on review").
- Anything already covered near-verbatim by an existing requirement — you
  don't have the existing requirements list here; that comparison is the
  reconcile step's job. When in doubt, extract it; reconcile will merge or
  drop the duplicate.

## Retrieval hints

For each candidate, write 2–5 short `hints` — phrasings of situations where
the requirement applies, in the words someone would use when they hit the
problem, not the words the requirement uses to state the rule. Hints are
embedded for semantic search but never shown to an agent directly, so this
is the one place you should reach for the *symptom*, not the *rule*:

- Requirement: "Never bump the major version unless explicitly told to."
  Hints: `"is this a major version bump"`, `"should I bump minor or major"`,
  `"breaking change version bump"`.
- Requirement: "The local adapter is for tests only; real runs use Docker."
  Hints: `"container runtime not starting"`, `"which runtime does a real run use"`.

Think about what an agent working on an unrelated-sounding task might type
or think when this requirement would actually matter, especially cases with
no word overlap with the requirement's own statement.

## Output

Write `$TEMP/candidates.json`:

```json
{
  "candidates": [
    {
      "statement": "Never bump the major version unless explicitly told to.",
      "rationale": "Major releases are batched manually at the maintainer's direction.",
      "scope": { "level": "domain", "domains": ["versioning"] },
      "hints": ["is this a major version bump", "breaking change version bump"],
      "confidence": "high",
      "provenance": {
        "kind": "doc",
        "ref": "CLAUDE.md#versioning",
        "extracted_at": "2026-09-13T10:00:00Z",
        "extracted_by": "intent-scan"
      }
    }
  ]
}
```

- `scope.level` is `"project"` (always injected — reserve this for
  genuinely project-wide rules) or `"domain"` (with `scope.domains` naming
  one or more entries from `domains.yaml`).
- `confidence` is `"high"` / `"medium"` / `"low"` — your judgment of how
  durable and unambiguous the source material makes this requirement.
- `provenance.kind` is `"doc"`, `"transcript"`, `"prompt"`, or `"commit"`
  matching where the material came from; `ref` is the file path (optionally
  `#heading`), run ID, task ID, or commit SHA; `extracted_at` is the current
  UTC time in RFC3339; `extracted_by` is `"intent-scan"`.
- Omit `candidates.json`'s `candidates` array entirely (`{"candidates": []}`)
  if nothing in the sources meets the bar above — that's a valid, expected
  outcome, not a failure.

## Results

- Emit `CLOCHE_RESULT:success` once `$TEMP/candidates.json` is written
  (even if it's an empty candidate list).
- Emit `CLOCHE_RESULT:fail` if the sources can't be read or the output can't
  be written.
