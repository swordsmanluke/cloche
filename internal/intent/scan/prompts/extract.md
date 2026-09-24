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
  original project-relative path. On a project with repositories configured
  via `[[repositories]]`, a doc from inside one is prefixed with its repo
  path (e.g. `docs/repos/anarkana/README.md` for the doc at
  `repos/anarkana/README.md`) — use that full path verbatim as `ref`.
- `commits.txt` — `<ref>\t<subject>` per new commit (noise already
  filtered), and `diffs/<short-sha>.patch` (or `diffs/<repo>-<short-sha>.patch`
  for a repo commit) for each. `ref` is already the exact string to use for
  `provenance.ref`: a bare SHA for the project's own history, or
  `<repo>@<sha>` (e.g. `repos/anarkana@abc1234...`) for a commit from a
  configured repository.
- `runs/<ref>/task_prompt.md` and `runs/<ref>/transcript.log` — task prompts
  and step-output transcripts from runs not yet mined. `ref` is the run ID,
  optionally prefixed with its repo (e.g. `repos/anarkana/run-42`) — use it
  verbatim as `provenance.ref`.
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

Write `$TEMP/candidates.csv` — a plain CSV, one row per candidate, with
exactly this header:

```csv
statement,rationale,scope_level,domains,hints,confidence,provenance_kind,provenance_ref
"Never bump the major version unless explicitly told to.","Major releases are batched manually at the maintainer's direction.",domain,versioning,"is this a major version bump;breaking change version bump",high,doc,CLAUDE.md#versioning
"Errors are one line: line N: <message>, never a traceback.",,project,,"traceback in output;why does the error have two lines",medium,commit,abc1234
```

- `scope_level` is `project` (always injected — reserve this for genuinely
  project-wide rules) or `domain`; `domains` then lists one or more names
  from `domains.yaml`, separated by `;` (leave it empty for `project`).
- `hints`: 2–5 phrases separated by `;`.
- `confidence` is `high` / `medium` / `low` — your judgment of how durable
  and unambiguous the source material makes this requirement.
- `provenance_kind` is `doc`, `transcript`, `prompt`, or `commit` matching
  where the material came from; `provenance_ref` is the file path under
  `docs/` (optionally `#heading`), the run ref under `runs/`, or the commit
  ref from `commits.txt` — copy it verbatim, repo prefix included.
- Standard CSV quoting: wrap a field in double quotes if it contains a
  comma, a double quote (write it as `""`), or a line break. Nothing else is
  special. Timestamps are added by the script; don't include them.
- If nothing in the sources meets the bar above, write the header line
  only — that's a valid, expected outcome, not a failure.

A script validates the file and reports back if anything is malformed; you
will get a chance to fix the CSV before the scan aborts. It then writes the
`candidates.json` the reconcile step reads — never write that file yourself.

## Results

- Emit `CLOCHE_RESULT:success` once `$TEMP/candidates.csv` is written
  (even if it's the header line only).
- Emit `CLOCHE_RESULT:fail` if the sources can't be read or the output can't
  be written.
