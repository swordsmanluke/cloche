# Design: Semi-Formal Code Review Workflow

## Overview

This document describes the design for an automated, agentic code review step integrated
into the cloche `develop` workflow. The review step acts as a hard gate between passing
tests and the `update-docs` step: it either approves the implementation or returns
structured, actionable findings that drive another implementation iteration.

The technique is based on *semi-formal reasoning* (Ugare & Chandra, Meta 2026), which
improves LLM code analysis accuracy by requiring agents to gather explicit evidence before
reaching any conclusion, rather than reasoning from surface-level impressions.

---

## Placement in the Workflow

```
implement -> verify-changes -> test -> code-review -> update-docs -> done
                                            |
                                     request-changes
                                            |
                                   update-task-prompt -> implement
                                            |
                                         give-up -> abort
```

The `code-review` step runs after `test:success`. It has `max_attempts = 3`. If the
implementation passes review, the workflow continues to `update-docs` as before. If
review finds blocking issues, the findings are appended to the task prompt and the
workflow loops back to `implement`. After three failed reviews the workflow aborts.

---

## Why Semi-Formal Reasoning

Standard ("free-form") agentic reasoning allows models to make claims about code behaviour
without verifying them. This is the dominant failure mode in code review: an agent skims
a diff, recognises a familiar pattern, and concludes correctness — missing shadowed
definitions, missing error propagation, or edge cases that only surface when execution
paths are traced.

Semi-formal reasoning addresses this by structuring the *input* side: the agent must fill
in a certificate template before it can report a verdict. Each section of the template
requires evidence gathered from the repository (file paths, line numbers, execution
traces). The agent cannot skip to a conclusion without first building the evidentiary
record.

Key properties enforced by the template:

- **No unsupported claims.** Every finding must cite a specific `file:line`.
- **Interprocedural tracing.** The agent follows call chains rather than assuming
  function behaviour from names or signatures.
- **Explicit counterexamples.** Blocking findings require a concrete scenario showing
  how the code diverges from requirements.
- **Separated concerns.** Correctness, error handling, and code quality are evaluated
  independently before a combined verdict is formed.

---

## The Certificate Template

The review prompt instructs the agent to fill in the following sections in order. The
agent may not emit a verdict until all sections are complete.

### Section 1 — Task Requirements

Extract and list the requirements from the task prompt. This anchors the rest of the
certificate to what was actually asked.

```
REQ R1: [requirement stated in task]
REQ R2: ...
```

### Section 2 — Changed Files

Enumerate every file changed in `git diff $(git merge-base HEAD main) HEAD`. For each:

```
FILE: [path]
CHANGES: [what was added, modified, or removed]
FUNCTIONS AFFECTED: [list with line numbers]
```

This step forces the agent to be aware of the full scope of the change before evaluating
any individual piece of it.

### Section 3 — Lens 1: Correctness

For each requirement R[N]:

```
CLAIM: This requirement is [MET / PARTIALLY MET / UNMET]
TRACE: [file:line] -> [file:line] -> ... (execution path demonstrating it)
EVIDENCE: [specific code cited]
GAPS: [any inputs, paths, or edge cases the implementation misses]
```

The `TRACE` field is the core of the semi-formal approach. It requires the agent to walk
function calls rather than assume behaviour, catching issues like name shadowing or
missing delegation.

### Section 4 — Lens 2: Error Handling

For each error path introduced or modified by the diff:

```
PATH: [where the error originates, file:line]
HANDLING: [returned / wrapped / logged / swallowed / panicked]
VERDICT: [ADEQUATE / INADEQUATE]
EVIDENCE: [file:line]
```

### Section 5 — Lens 3: Code Quality

For each modified type, function, or interface:

```
ITEM: [name at file:line]
CONCERNS: [specific issue if any — e.g. "exported field on unexported type", "error
           return ignored at call site"] or NONE
EVIDENCE: [file:line]
```

This lens covers OOP / idiomatic Go practices, naming, visibility, and structural issues.
It does not duplicate the correctness or error-handling lenses.

### Section 6 — Findings

Synthesise all issues across lenses into a ranked list:

```
FINDING F1: [BLOCKING | ADVISORY] [description] [file:line]
FINDING F2: ...
```

`BLOCKING` findings prevent approval. `ADVISORY` findings are included in the feedback
but do not block. If there are no findings, this section is explicitly stated as empty.

### Section 7 — Formal Conclusion

```
All requirements met:          [YES / NO]
All error paths adequate:      [YES / NO]
No blocking quality issues:    [YES / NO]

VERDICT: [APPROVE / REQUEST_CHANGES]
```

The verdict must be consistent with the findings. The agent then emits the appropriate
`CLOCHE_RESULT` marker.

---

## Feedback Loop

When the verdict is `REQUEST_CHANGES`:

1. The `code-review` step writes the structured findings (Section 6 + Section 7) to
   `$(clo get temp_file_dir)/review_feedback.md`. This is a clean file containing only
   the actionable findings, not the full reasoning trace.

2. The step sets `clo set review_feedback "$(clo get temp_file_dir)/review_feedback.md"`
   so the next step can locate the file.

3. The `update-task-prompt` script step reads the feedback file and appends a
   `## Code Review Findings (Attempt N)` section to the task prompt at
   `$(clo get task_prompt_path)`. The original requirements are preserved; the findings
   are additive.

4. The workflow transitions back to `implement`. The implementation agent sees both the
   original requirements and the explicit review objections, with file and line citations,
   in its task prompt.

On each subsequent review attempt, the previous feedback section is already present in
the task prompt. The new review overwrites `review_feedback.md` and appends a new dated
section — so the implementation agent always has the full history of review feedback when
it reads the prompt.

After `max_attempts = 3` failed reviews, the engine emits `give-up` automatically (before
the fourth agent invocation) and the workflow aborts.

---

## Dependency on temp_file_dir

The feedback file path relies on the `temp_file_dir` built-in KV key, which the cloche
daemon sets automatically at run start (see `docs/design/...` for that feature). This
gives the review step a stable, run-scoped scratch location without hardcoding paths.

Container steps read the value with `clo get temp_file_dir`. The directory is inside
`.cloche/runs/<run-id>/` and is already covered by the standard gitignore pattern.

---

## Workflow DSL Changes

```hcl
step code-review {
  prompt       = file(".cloche/prompts/code-review.md")
  timeout      = "30m"
  max_attempts = 3
  results      = [approve, request-changes, give-up]
}

step update-task-prompt {
  run     = "bash .cloche/scripts/update-task-prompt.sh"
  results = [success, fail]
}

test:success              -> code-review
code-review:approve       -> update-docs
code-review:request-changes -> update-task-prompt
code-review:give-up       -> abort
update-task-prompt:success -> implement
update-task-prompt:fail   -> abort
```

---

## Files To Create / Modify

| File | Change |
|------|--------|
| `.cloche/develop.cloche` | Add `code-review` and `update-task-prompt` steps and wiring |
| `.cloche/prompts/code-review.md` | New — the semi-formal certificate prompt |
| `.cloche/scripts/update-task-prompt.sh` | New — appends review findings to task prompt |

The `implement` prompt requires no changes: it already reads the full task prompt file,
which will now contain review findings when they exist.

---

## Design Decisions

**Single agent, multiple lenses** — The review uses one agent step with a structured
multi-section template rather than parallel subagents per lens. A single agent can
correlate findings across lenses (e.g. a correctness gap and an error-handling gap in the
same code path). Parallel subagents would require a collation step and cannot share
intermediate observations. The KISS principle favours one well-structured prompt.

**Findings written to a file, not the KV store** — Review findings can easily exceed the
1 KB KV value limit. The feedback file is written to `temp_file_dir` and only the path is
stored in the KV store, following the same pattern as `task_prompt_path`.

**Feedback is appended, not replaced** — The task prompt accumulates review history
across attempts. This ensures the implementation agent has full context on all prior
objections and cannot re-introduce a previously rejected pattern without the reviewer
explicitly approving the change.

**Advisory findings do not block** — Only `BLOCKING` findings trigger `REQUEST_CHANGES`.
Advisory findings are included in the feedback file so the implementation agent is aware
of them, but the workflow proceeds to `update-docs` if all blocking issues are clear.
