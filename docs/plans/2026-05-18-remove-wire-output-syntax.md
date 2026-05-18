# Wire Output Mapping Removal Plan

**Date:** 2026-05-18  
**Status:** COMPLETE — Syntax fully removed as of 2026-04-14. This document catalogs the removal and documents remaining cleanup.

## Overview

The wire output mapping syntax — `step:result -> next [ VAR = output.field ]` — was proposed in `/docs/plans/2026-03-09-wire-output-mapping-design.md` but was never fully implemented. The design was superseded on 2026-04-14, and all code support was removed across three commits on that date. The feature has been completely excised from the codebase.

**Current Verdict:** The syntax is **fully removed** and non-functional. No active code paths support it.

## Removal Timeline

All three removal commits happened on **2026-04-14**:

1. **Commit 3106e73** (`09:24:36 UTC`)  
   *Removes wire output mapping syntax from DSL parser*
   - Deleted `parseOutputMappings()` function and all mapping parsing logic
   - Modified `parseWire()` to remove mapping bracket parsing
   - Removed 34 lines of documentation from `docs/workflows.md`
   - Removed 169 lines of mapping-related tests from `internal/dsl/parser_test.go`

2. **Commit 0fe3c40** (`09:37:01 UTC`)  
   *Removes domain types and executor support*
   - Deleted `internal/domain/output_path_test.go` entirely (211 lines)
   - Removed `OutputMapping`, `OutputPath`, `PathSegment` types from `internal/domain/workflow.go`
   - Removed `OutputMap` field from `Wire` struct
   - Removed `Evaluate()` method from output paths
   - Cleaned up host executor mapping resolution logic
   - Removed 110 lines of mapping tests from `internal/domain/workflow_test.go`
   - Updated design doc header to mark it "Superseded"

3. **Commit 58e52b7** (`09:26:13 UTC`)  
   *Removes documentation and remaining executor code*
   - Removed 37 additional lines from `docs/workflows.md` (Wire Output Mappings section)
   - Cleaned up 3 lines in `docs/USAGE.md`
   - Removed 35 lines of mapping resolution code from `internal/host/executor.go`
   - Removed 201 lines of executor mapping tests from `internal/host/executor_test.go`

## Code References

### Parser: No Support

**File:** `/home/lucas/workspace/wrapped_cloche/repos/cloche/internal/dsl/parser.go`  
**Lines:** 592–619  
**Function:** `parseWire()`

```go
func (p *Parser) parseWire() (domain.Wire, error) {
	fromTok := p.current
	p.advance()

	if _, err := p.expect(TokenColon); err != nil {
		return domain.Wire{}, err
	}

	resultTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	if _, err := p.expect(TokenArrow); err != nil {
		return domain.Wire{}, err
	}

	toTok, err := p.expect(TokenIdent)
	if err != nil {
		return domain.Wire{}, err
	}

	return domain.Wire{
		From:   fromTok.Literal,
		Result: resultTok.Literal,
		To:     toTok.Literal,
	}, nil
}
```

**Status:** Parser ends immediately after parsing `TO` identifier. No bracket-parsing code.

### Domain Types: Removed

**File:** `/home/lucas/workspace/wrapped_cloche/repos/cloche/internal/domain/workflow.go`  
**Lines:** 52–57  
**Type:** `Wire`

```go
type Wire struct {
	From     string
	Result   string
	To       string
	Implicit bool
}
```

**Status:** `Wire` struct has no `OutputMap` field. No `OutputMapping`, `OutputPath`, or `PathSegment` types exist anywhere in the domain package.

### Runtime: No Executor Code

**File:** `/home/lucas/workspace/wrapped_cloche/repos/cloche/internal/host/executor.go`  
**Status:** No references to output mapping evaluation. The previous `resolveOutputMappings()` function has been completely removed.

**Verification:**
```bash
$ grep -n "OutputMapping\|resolveOutputMappings\|output\." internal/host/executor.go
(no results)
```

## Test References

All tests for the output mapping feature have been removed:

### Deleted Test Files

1. **`internal/domain/output_path_test.go`** (211 lines)  
   - Deleted entirely in commit 0fe3c40
   - Tested `OutputPath.Evaluate()` method
   - Tested path parsing and JSON navigation

### Deleted Test Cases

1. **`internal/dsl/parser_test.go`** (169 lines removed in 3106e73)  
   - Removed: `TestParseWire` cases that tested bracket syntax
   - Removed: `TestParseOutputMappings` and `TestParseOutputPath` test functions

2. **`internal/domain/workflow_test.go`** (110 lines removed in 0fe3c40)  
   - Removed: Tests for `OutputMapping` struct construction
   - Removed: Tests for duplicate mapping validation

3. **`internal/host/executor_test.go`** (201 lines removed in 58e52b7)  
   - Removed: `TestResolveOutputMappings` test cases
   - Removed: Tests for JSON path evaluation during step execution

**Verification:** No test files currently reference `OutputMapping`, `OutputPath`, or output mapping syntax:
```bash
$ grep -r "OutputMapping\|output\.field\|VAR = output" --include="*_test.go"
(no results)
```

## Documentation References

### Removed from Active Documentation

1. **`docs/workflows.md`**  
   - **Removed in commit 3106e73:** "Wire Output Mappings" section (34 lines)  
   - **Removed in commit 58e52b7:** "Wire Output Mappings" section (37 additional lines)  
   - Total removal: 71 lines of DSL reference documentation

2. **`docs/USAGE.md`**  
   - **Removed in commit 58e52b7:** Example showing output mapping syntax (3 lines)

### Stale References (Informational Only)

1. **`docs/plans/2026-03-09-wire-output-mapping-design.md`** (entire file)  
   - **Status:** Marked "Superseded (2026-04-14): OutputMapping/OutputPath/PathSegment types were removed; this design was never implemented." at the top.
   - **Content:** Full design document with syntax, implementation plan, proto definitions.
   - **Action:** Kept for historical reference; no changes needed.

2. **`docs/plans/2026-03-24-workflow-refactor-design.md`**  
   - **Line 169:** Comment `// step output content (for output mappings)` in proto `StepResult` message
   - **Line 412:** "output mappings all remain the same" in constraints section
   - **Context:** Written before removal; design is dated 2026-03-24, removal happened 2026-04-14
   - **Status:** Stale but historical reference doc; no active impact

3. **`CHANGELOG.md`** (v3.14.0 section)  
   - **Line 99:** "DEPRECATION: Wire output mapping syntax..."
   - **Status:** Correct historical record; kept for release notes

4. **`docs/CHANGELOG-DETAILED.md`**  
   - **Lines 135–139:** Detailed removal entries
   - **Status:** Correct; documents the three removal commits

### README.md Vestigial Reference

**File:** `README.md`  
**Line:** ~16  
**Text:** "parallel branches, collect/join, wire output mappings, host workflows, and"

**Status:** Stale — references a removed feature in the feature list. This is the only lingering active-documentation error.

## In-Use Workflows

**Search Results:** No `.cloche` workflow files use the removed syntax.

```bash
$ find /home/lucas/workspace/wrapped_cloche -name "*.cloche" \
  -exec grep -l "\[.*output\|VAR = output\|KEY = output" {} \;
(no results)
```

### Workflows Checked

- Cloche internal: `/home/lucas/workspace/wrapped_cloche/repos/cloche/.cloche/*.cloche`
- Cloche examples: `/home/lucas/workspace/wrapped_cloche/repos/cloche/examples/**/*.cloche`
- Wrapper repo: `/home/lucas/workspace/wrapped_cloche/.cloche/*.cloche`

**All clean — no active use of the syntax.**

## Removal Plan: Remaining Tasks

### ✓ Complete

- [x] Parser: removed mapping parsing logic
- [x] Domain types: removed `OutputMapping`, `OutputPath`, `PathSegment`
- [x] Executor: removed mapping evaluation code
- [x] Tests: removed all test cases and dedicated test files
- [x] Workflow documentation: removed DSL reference sections

### Pending

Only **one minor cleanup item** remains:

1. **Update README.md (1 file, ~1 line)**  
   - **File:** `README.md`  
   - **Action:** Remove "wire output mappings," from the feature list on line ~16  
   - **Current text:** "See [docs/workflows.md](docs/workflows.md) for the full DSL reference including parallel branches, collect/join, wire output mappings, host workflows, and container configuration."  
   - **Updated text:** "See [docs/workflows.md](docs/workflows.md) for the full DSL reference including parallel branches, collect/join, host workflows, and container configuration."  
   - **Rationale:** References a removed feature; no longer accurate

### Historical References (No Action Needed)

- `docs/plans/2026-03-09-wire-output-mapping-design.md` — Keep as-is for historical record  
- `docs/plans/2026-03-24-workflow-refactor-design.md` — Keep as-is; stale but not active  
- `CHANGELOG.md` / `docs/CHANGELOG-DETAILED.md` — Keep as-is for release history

## Migration Path for Users

For users with existing workflows that used the syntax (as documented in the April 2026 release notes):

**Old pattern:**
```
step-a:success -> step-b [ CLOCHE_TASK_ID = output[0].id, CLOCHE_TASK_TITLE = output[0].title ]
```

**New pattern (using KV store):**

In step-a (e.g., a script step):
```bash
cloche set task_id "$(echo "$json" | jq -r '.[0].id')"
cloche set task_title "$(echo "$json" | jq -r '.[0].title')"
```

In step-b (e.g., another script step):
```bash
CLOCHE_TASK_ID=$(cloche get task_id)
CLOCHE_TASK_TITLE=$(cloche get task_title)
```

This approach was documented in the v3.14.0 CHANGELOG and is the official replacement pattern.

## Conclusion

The wire output mapping feature has been **fully removed** from the codebase. No parser, domain, runtime, or test code remains. The only remaining trace is a minor vestigial README.md reference and historical design/changelog documentation, which are correct as-is.

The only action item is to update README.md to remove the stale "wire output mappings" reference from the feature list.
