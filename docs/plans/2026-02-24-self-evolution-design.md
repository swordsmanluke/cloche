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

### Stage 4: Executor Branches

Based on each lesson's `category`, it routes to one of three branches.

#### Branch A: Prompt Curator (LLM + deterministic dedup)

Handles `prompt_improvement` lessons.

- Receives the current prompt file content and the lesson
- ACE-style curation: merges the lesson into the prompt as a structured
  bullet/rule
- Deduplication: checks for semantic overlap with existing bullets; if a lesson
  refines an existing bullet, it updates in place rather than appending
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
them to the `.cloche` workflow file. Three operations:

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

Appends wires to the wiring section. If a wire source already has a target, the
new wire creates a fanout (both targets fire — already supported by the engine).

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
2. **Log** — appends a JSONL entry to `.cloche/evolution/log.jsonl` and inserts
   into the `evolution_log` SQLite table
3. **Knowledge base update** — appends the Reflector's lessons to
   `knowledge/<workflow>.md`, runs deduplication/refinement

## Knowledge Base Structure

The knowledge base is an ACE-style structured document — itemized bullets with
metadata, not prose. Each bullet tracks usefulness over time:

```markdown
# Knowledge Base: develop workflow

## Prompt Insights

- **[P001]** (applied: 3, helpful: 2, stale: 0) Always sanitize user inputs
  with html.EscapeString() before template rendering. XSS vulnerabilities
  were recurring in form handlers.
  _Evidence: run-abc, run-def, run-ghi, run-jkl_

- **[P002]** (applied: 1, helpful: 1, stale: 0) Use goimports ordering for
  imports. Lint step flags this in ~60% of runs.
  _Evidence: run-mno, run-pqr_

## Workflow Insights

- **[W001]** (applied: 1, helpful: 1, stale: 0) Added gosec security scan
  step. Catches static security issues before deployment.
  _Evidence: run-abc, run-def_

## Failure Patterns

- **[F001]** (occurrences: 8) Test timeout failures correlate with database
  connection setup. Usually resolves on retry.
  _Action: none (transient)_

- **[F002]** (occurrences: 4) Valgrind reports unfreed memory in request
  handlers. Consistent pattern — agent needs explicit reminder about cleanup
  in defer/finally blocks.
  _Action: prompt update P001 applied_
```

The `applied`/`helpful`/`stale` counters enable future refinement. If a lesson
is applied but the same failure keeps occurring, it can be revised or removed.

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
