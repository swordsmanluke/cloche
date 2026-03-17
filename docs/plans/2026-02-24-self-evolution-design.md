# Self-Evolution System Design

## Overview

Cloche's self-evolution system analyzes workflow run history and user feedback to
automatically improve prompts, add validation steps, and refine workflows over
time. It is a daemon-side post-run pipeline that triggers after every workflow
completion, applies changes directly to project files, and maintains a structured
audit trail.

The system draws on the ACE (Agentic Context Engineering) framework for prompt
evolution — treating prompts as structured, itemized playbooks that accumulate
lessons rather than monolithic documents that get rewritten.

**Scope (this version):** Additive changes only. Prompt updates, new script
steps, new agent steps, new wiring. No step removal or mutation of existing step
definitions.

## Architecture

The evolution system is a multi-stage pipeline with three specialized output
branches. The LLM handles classification, reflection, and content generation.
Deterministic Go code handles structural manipulation (DSL editing, file I/O,
audit logging).

```
                    ┌─────────────────────────────────────┐
                    │         Workflow Run Completes       │
                    └──────────────┬──────────────────────┘
                                   │
                    ┌──────────────▼──────────────────────┐
                    │           Collector                  │
                    │  Gathers: run results, history.log,  │
                    │  step outputs, knowledge base,       │
                    │  current prompts + workflow          │
                    └──────────────┬──────────────────────┘
                                   │
                    ┌──────────────▼──────────────────────┐
                    │         Classifier (LLM)            │
                    │  Categorizes run prompt:             │
                    │  bug | feedback | feature |          │
                    │  enhancement | chore                 │
                    └──────────────┬──────────────────────┘
                                   │
                    ┌──────────────▼──────────────────────┐
                    │         Reflector (LLM)              │
                    │  ACE-style: examines traces,         │
                    │  extracts structured lessons          │
                    │  (bullets with metadata)              │
                    └──────────────┬──────────────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                     │
   ┌──────────▼────────┐ ┌────────▼──────────┐ ┌───────▼──────────┐
   │  Prompt Curator    │ │  Script Generator │ │  DSL Mutator     │
   │  (LLM + dedup)     │ │  (LLM)            │ │  (Go code)       │
   │                    │ │                    │ │                   │
   │  Merges lessons    │ │  Writes new        │ │  Adds steps,     │
   │  into prompt files │ │  checker/linter    │ │  wiring, collects │
   │  as structured     │ │  scripts           │ │  to .cloche file  │
   │  bullets           │ │                    │ │                   │
   └────────┬───────────┘ └────────┬───────────┘ └───────┬──────────┘
            │                      │                      │
            └──────────────────────┼──────────────────────┘
                                   │
                    ┌──────────────▼──────────────────────┐
                    │         Audit Logger                 │
                    │  Records all changes + reasons        │
                    │  Updates knowledge base summary       │
                    │  Snapshots pre-change state           │
                    └─────────────────────────────────────┘
```

The three branches fire independently based on lesson category. A single
evolution pass may produce changes across multiple branches, or none at all.

## Multi-Project Scoping

The daemon supports multiple concurrent cloche instances across different
projects (and multiple runs within the same project). Every run record includes
`project_dir` — the absolute path to the project directory where `cloche run`
was invoked.

All evolution state is scoped by project_dir + workflow_name:
- SQLite queries filter by project_dir
- Knowledge bases live at `<project_dir>/.cloche/evolution/knowledge/<workflow>.md`
- Evolution passes for different projects/workflows run independently
- One evolution pass at a time per project+workflow (mutex)

## Data Collection

### New Signals Captured Per Run

| Signal               | Where captured                        | Notes                          |
|----------------------|---------------------------------------|--------------------------------|
| Assembled prompt     | Prompt adapter, before LLM invocation | Full text sent to the agent    |
| Agent stdout         | Prompt adapter, after agent returns   | Full LLM response              |
| Run prompt class     | Classifier LLM, post-run             | bug/feedback/feature/etc.      |
| Retry count per step | Prompt adapter attempt tracking       | Already in filesystem, add to SQLite |
| Project directory    | CLI sends to daemon via gRPC          | Currently implicit             |

### Run Prompt Classification

The Classifier examines each run's `--prompt` text and categorizes it:

- **bug** — fixing something broken; indicates a gap the system should reflect on
  for patterns in its approach to software development
- **feedback** — code review style issues (DRY, SOLID, architecture); things
  caught in a typical review cycle that the system should watch its output for
- **feature** — new functionality; normal work
- **enhancement** — improving existing functionality
- **chore** — maintenance; no evolution signal

Bug and feedback runs are the primary evolution drivers. Features and
enhancements provide background context but don't typically trigger changes.
Classification happens via LLM inference on the prompt text — no explicit
`--type` flag needed from the user.

### SQLite Schema Additions

```sql
-- Add project scoping to runs
ALTER TABLE runs ADD COLUMN project_dir TEXT NOT NULL DEFAULT '';

-- Extend step_executions with captured data
ALTER TABLE step_executions ADD COLUMN prompt_text TEXT;
ALTER TABLE step_executions ADD COLUMN agent_output TEXT;
ALTER TABLE step_executions ADD COLUMN attempt_number INTEGER DEFAULT 1;

-- Track evolution passes
CREATE TABLE evolution_log (
    id TEXT PRIMARY KEY,
    project_dir TEXT NOT NULL,
    workflow_name TEXT NOT NULL,
    trigger_run_id TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    classification TEXT,
    changes_json TEXT NOT NULL,
    knowledge_delta TEXT
);
```

### On-Disk Evolution State

```
.cloche/
  evolution/
    log.jsonl                   # append-only audit trail (one JSON object per line)
    knowledge/
      develop.md                # ACE-style knowledge base for 'develop' workflow
    snapshots/
      <timestamp>-<filename>    # pre-change backups for revert
    population/
      <step-name>/
        candidate-NNN.md        # candidate prompt variant
        meta.jsonl              # candidate metadata (id, status, parent, score)
        assignments.jsonl       # run→candidate mapping
```

## Pipeline Stages

### Stage 1: Collector

Triggers after every run completion. Gathers:

1. All runs for this project+workflow since the last evolution pass (tracked via
   `evolution_log.trigger_run_id`)
2. The accumulated knowledge base (`knowledge/<workflow>.md`)
3. Current prompt files referenced by the workflow
4. The current `.cloche` workflow DSL file
5. Runs classified as "bug" or "feedback" carry more weight in analysis

If there are no new runs since the last evolution, the pipeline short-circuits.

### Stage 2: Classifier (LLM)

For each unclassified run, the Classifier examines the run prompt and assigns a
category. The classification is stored in the run record (SQLite) for use by the
Reflector and for future queries.

### Stage 3: Reflector (LLM)

The Reflector examines execution traces and extracts structured lessons. It
receives the collected run data (step results, failure logs, retry counts), the
current knowledge base, and the current prompts and workflow.

It produces delta entries — structured lessons:

```json
{
  "lessons": [
    {
      "id": "lesson-20260224-001",
      "category": "prompt_improvement",
      "target": "prompts/implement.md",
      "insight": "Agent consistently produces unsanitized HTML output in form handlers. 4 of last 6 bug-type runs involved XSS.",
      "suggested_action": "Add explicit rule: always sanitize user inputs with html.EscapeString() before template rendering",
      "evidence": ["run-abc", "run-def", "run-ghi", "run-jkl"],
      "confidence": "high"
    },
    {
      "id": "lesson-20260224-002",
      "category": "new_step",
      "step_type": "script",
      "insight": "No static security analysis in workflow. Security bugs only caught by user reports post-deployment.",
      "suggested_action": "Add gosec script step after test, wire failures to fix",
      "evidence": ["run-abc", "run-def"],
      "confidence": "medium"
    }
  ]
}
```

Confidence levels (high/medium/low) are based on evidence count and pattern
consistency. Only lessons meeting the configured minimum confidence proceed to
the executor branches.

**Knowledge base deduplication:** After confidence filtering, the Reflector
cross-references generated lesson IDs against the current knowledge base text.
Lessons whose ID already appears in the knowledge base are dropped, preventing
the same lessons from being re-surfaced across evolution cycles. The
Orchestrator applies a second-layer ID filter as a guard, ensuring that even if
the Reflector misses a duplicate, previously applied lessons are not
re-processed.

### Stage 4: Executor Branches

Based on each lesson's `category`, it routes to one of three branches.

#### Branch A: Prompt Curator (LLM + deterministic dedup)

Handles `prompt_improvement` lessons.

- Receives the current prompt file content and the lesson
- **Content-change guard:** Before calling the LLM, the curator checks whether
  the lesson's key insight or suggested action is already present in the current
  prompt text (case-insensitive substring match). If the lesson is already
  incorporated, the curator returns immediately without calling the LLM and
  without rewriting the file. This prevents churn, snapshot bloat, and
  regression risk from unnecessary rewrites.
- ACE-style curation: merges the lesson into the prompt as a structured
  bullet/rule
- Deduplication: checks for semantic overlap with existing bullets; if a lesson
  refines an existing bullet, it updates in place rather than appending
- Code-fence stripping: LLM responses are cleaned of markdown code fences
  (` ``` `) before writing, extracting only the content between fences. This
  prevents meta-commentary or formatting artifacts from leaking into prompt files.
- Conversational response validation: after stripping code fences, the output is
  checked for conversational markers (e.g. "I need write permission", "Here is
  the updated prompt:", "Could you grant access"). If the LLM returned
  meta-conversation text instead of prompt content, the curator falls back to
  appending the lesson directly as a structured bullet rather than trusting the
  LLM output. This prevents prompt file corruption when the LLM produces
  interactive-style responses.
- **Post-write sanity check:** After writing the updated prompt to disk, the
  curator re-reads the file and validates it: the content must be non-empty,
  must not start with conversational phrases, and must contain at least one
  markdown heading (`#`). If the sanity check fails, the curator restores the
  file from the pre-change snapshot and returns a `prompt_update_rollback`
  change entry instead of `prompt_update`. The snapshot is preserved as
  evidence.
- Output: the modified prompt file content, written to disk

#### Branch B: Script Generator (LLM)

Handles `new_step` lessons where step_type is "script" or "agent".

- Receives the lesson describing what check or analysis is needed
- Generates a script file (shell, Python, etc.) implementing the check, or a
  prompt file for agent steps
- Script/prompt is written to the project directory (e.g.,
  `scripts/security-scan.sh` or `prompts/review.md`)
- Output: the file path and content

#### Branch C: DSL Mutator (deterministic Go code)

Handles structural workflow changes. Receives structured proposals and applies
them to the `.cloche` workflow file. Four operations:

**Add Step Definition:**
```json
{
  "op": "add_step",
  "name": "security-scan",
  "type": "script",
  "config": {
    "run": "gosec ./...",
    "results": ["success", "fail"]
  }
}
```

Parses the workflow file, inserts the step definition, serializes back.

**Add Wiring:**
```json
{
  "op": "add_wire",
  "wires": [
    {"from": "test", "result": "success", "to": "security-scan"},
    {"from": "security-scan", "result": "fail", "to": "fix"}
  ]
}
```

Appends wires to the wiring section.

**Rewire Result:**
```json
{
  "op": "rewire_result",
  "from": "test",
  "result": "success",
  "old_to": "done",
  "new_to": "security-scan"
}
```

Changes the target of an existing wire. Used by the orchestrator when inserting
a new step into the graph: it finds a wire pointing to `done`, rewires it to
the new step, then wires the new step's results to terminals. This splices the
new step into the existing flow rather than creating a disconnected node.

**Update Collect:**
```json
{
  "op": "update_collect",
  "collect_target": "done",
  "add_conditions": [
    {"step": "security-scan", "result": "success"}
  ]
}
```

Finds the matching `collect` clause and adds the new condition.

**Validation gate:** After any mutation, the mutator calls
`domain.Workflow.Validate()` on the result. If validation fails, the mutation is
rejected, the original file is preserved, and the failure is logged.

**DSL Serializer:** The mutator requires a round-trip-preserving serializer —
parse into an AST that retains comments and whitespace, modify the AST, serialize
back. This preserves user formatting and comments.

Branches B and C often fire together — a new script step needs both the script
file (B) and the DSL wiring (C).

### Stage 5: Audit Logger

After all changes are applied:

1. **Snapshot** — copies pre-change versions of all modified files to
   `.cloche/evolution/snapshots/`
2. **Restore** — restores a file from a named snapshot (used by the Curator
   to roll back corrupted writes)
3. **Log** — appends a JSONL entry to `.cloche/evolution/log.jsonl` and inserts
   into the `evolution_log` SQLite table
4. **Knowledge base update** — merges the Reflector's lessons into
   `knowledge/<workflow>.jsonl` with ID-based deduplication and optional
   pruning via `MaxPromptBullets`

## Knowledge Base Structure

The knowledge base is a JSONL file — one JSON object per line, each
representing a `Lesson`. Lessons are keyed by `id` for deduplication: if a
lesson with the same ID already exists, it is updated in place rather than
appended. When `MaxPromptBullets` is configured and the total number of
lessons exceeds that limit, the oldest entries are pruned to stay within
bounds.

```jsonl
{"id":"P001","category":"prompt_improvement","insight":"Always sanitize user inputs with html.EscapeString() before template rendering","suggested_action":"Add sanitization rule to prompt","evidence":["run-abc","run-def"],"confidence":"high"}
{"id":"W001","category":"new_step","step_type":"script","insight":"Added gosec security scan step","suggested_action":"Add gosec step to workflow","evidence":["run-abc","run-def"],"confidence":"high"}
{"id":"F001","category":"prompt_improvement","insight":"Test timeout failures correlate with database connection setup","suggested_action":"Add retry guidance to prompt","evidence":["run-ghi","run-jkl"],"confidence":"medium"}
```

The structured JSONL format enables reliable deduplication by lesson ID and
programmatic querying. Lessons that are superseded by newer insights with the
same ID are replaced automatically.

## Trigger Logic

### When Evolution Runs

The pipeline triggers in the daemon after every workflow run completes (success
or failure). Smart throttling prevents waste:

- **Debounce:** If runs complete in rapid succession (parallel runs on the same
  project), wait a configurable period (default 30s) after the last completion
  before triggering. This batches multiple runs into one analysis pass.
- **Mutex:** If an evolution pass is already in progress for this
  project+workflow, skip. The next run completion will pick up the slack.
- **No-op detection:** If the Reflector finds nothing actionable (clean feature
  runs, no patterns), the pipeline logs "no changes" and exits without firing
  the executor branches.

### History Window

The Collector gathers all runs since the last evolution pass (tracked via
`evolution_log.trigger_run_id`). The knowledge base provides the long-term
memory — once raw runs are analyzed, their lessons persist in the knowledge base
even as the raw data ages out. This gives the system both recency (raw run data)
and continuity (accumulated knowledge).

## Protocol Changes

### gRPC (CLI to Daemon)

```protobuf
message RunRequest {
  string workflow = 1;
  string prompt = 2;
  string project_dir = 3;   // NEW
}
```

### Status Protocol (Agent to Daemon)

```protobuf
message StepStarted {
  string step_name = 1;
  string prompt_text = 2;    // NEW: assembled prompt
}

message StepCompleted {
  string step_name = 1;
  string result = 2;
  string agent_output = 3;   // NEW: LLM response
  int32 attempt_number = 4;  // NEW
}
```

### Prompt Adapter Changes

The prompt adapter captures the assembled prompt text before invoking the agent
and the agent's stdout after it returns. Both are forwarded to the daemon via
the status protocol. No changes to prompt assembly logic — the evolution system
modifies prompt files on disk, and the existing `file()` mechanism picks up
changes on the next run.

## Configuration

Per-project in `.cloche/config`:

```toml
[evolution]
enabled = true                # master switch
debounce_seconds = 30         # wait after last run before analyzing
min_confidence = "medium"     # minimum lesson confidence to act on
max_prompt_bullets = 50       # cap on knowledge bullets per prompt
```

Evolution is enabled by default. Users can disable it per-project for static
workflows.

## Future Work (Out of Scope)

- **Step removal/mutation** — modifying or removing existing step definitions
  (higher risk, needs careful design around backwards compatibility)
- **`cloche evolve --revert <evo-id>`** — automated revert from snapshots
- **Helpfulness tracking** — automatically detecting whether applied lessons
  actually reduced failure rates, and pruning unhelpful ones
- **Cross-project learning** — sharing knowledge base insights across projects
