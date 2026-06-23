Feature: Per-step token metrics design document
  As a cloche developer implementing durable token-usage observability
  I want a complete design document at docs/plans/2026-05-28-step-token-metrics.md
  So that the storage schema, query shapes, and CLI surface can be implemented
  without further design review

  # ─── L1: Schema and storage design ─────────────────────────────────────────

  Scenario: Design doc exists at the expected path
    Given the step-token-metrics design doc
    Then the step-token-metrics design doc file exists

  Scenario: Step identifier section specifies the canonical key
    Given the step-token-metrics design doc
    Then the design doc has a step identifier section
    And the step identifier section defines the canonical key as workflow name plus step name
    And the step identifier section reconciles with the run-state step-view design

  Scenario: Schema section defines a concrete field list
    Given the step-token-metrics design doc
    Then the design doc has a schema section
    And the schema includes an input_tokens field
    And the schema includes an output_tokens field
    And the schema includes a timestamp field
    And the schema includes a workflow name scope field
    And the schema includes a step name scope field

  Scenario: Host vs container coverage is addressed
    Given the step-token-metrics design doc
    Then the design doc has a host vs container section
    And the host vs container section confirms or documents gaps in coverage

  # ─── L2: Query shapes and CLI surface ───────────────────────────────────────

  Scenario: Slice-by-step query shape has a concrete example
    Given the step-token-metrics design doc
    Then the design doc has a query shapes section
    And the query shapes section includes a slice-by-step query with a concrete example

  Scenario: Aggregate-by-workflow query shape has a concrete example
    Given the step-token-metrics design doc
    Then the design doc has a query shapes section
    And the query shapes section includes an aggregate-by-workflow query with a concrete example

  Scenario: Trend-over-time query shape has a concrete example
    Given the step-token-metrics design doc
    Then the design doc has a query shapes section
    And the query shapes section includes a trend-over-time query with a concrete example

  Scenario: CLI surface section names specific command forms
    Given the step-token-metrics design doc
    Then the design doc has a CLI surface section
    And the CLI surface section names a cloche metrics command

  Scenario: Storage section reconciles with the metrics-reporting design
    Given the step-token-metrics design doc
    Then the design doc has a storage section
    And the storage section references the metrics table from the metrics-reporting design

  Scenario: No open questions remain unresolved
    Given the step-token-metrics design doc
    Then the design doc has no unresolved open questions
