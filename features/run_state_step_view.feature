Feature: Run-state per-step view design doc

  The run-state web UI must expose a flat view of every step in a run,
  using fully-qualified step names, chronological ordering, and a stable
  color system to distinguish workflow segments. This feature tracks the
  completeness of the design document that specifies that view.

  Background:
    Given the run-state step-view design doc at "docs/plans/2026-05-28-run-state-step-view.md"

  # ──── L1: Data model ────────────────────────────────────────────────────────

  Scenario: Design doc exists and is non-empty
    Then the design doc file exists and is non-empty

  Scenario: Row schema covers all required columns
    Then the design doc contains a "Row Schema" section
    And the row schema names the columns "step", "result", "started", "duration"

  Scenario: Canonical step identifier uses fully-qualified format
    Then the design doc specifies the step FQN format as "workflow:subworkflow:step"

  Scenario: Data source endpoint is specified
    Then the design doc names "GET /api/runs/{id}" as the data source endpoint

  # ──── L2: UI presentation ───────────────────────────────────────────────────

  Scenario: Step ordering is chronological by started_at
    Then the design doc specifies step ordering as chronological by "started_at"
    And the design doc documents handling for pending steps with no "started_at"

  Scenario: Color assignment is hash-based and stable
    Then the design doc specifies a hash-based color assignment strategy
    And the design doc names CSS variables "--wf-1-border" through "--wf-6-border"

  Scenario: Palette exhaustion is handled by wrapping
    Then the design doc documents color palette wrapping beyond 6 workflow segments

  Scenario: Nesting uses color and indent with truncation for long names
    Then the design doc specifies a nesting strategy using color and indent
    And the design doc specifies truncation for long qualified names
    And the design doc specifies a hover title attribute for full qualified names
