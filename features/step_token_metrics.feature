Feature: Per-step token metrics
  As a developer using Cloche
  I want to query token usage broken down by workflow step
  So that I can identify which steps are most expensive and track changes over time

  Background:
    Given the daemon is running against a test project directory

  # ─── Slice by step ───────────────────────────────────────────────────────────

  Scenario: cloche metrics shows token totals for a specific workflow step
    Given the project has completed runs of the "develop" workflow
    And the "implement" step used 10000 input tokens and 2000 output tokens in one run
    And the "implement" step used 8000 input tokens and 1800 output tokens in another run
    When the user runs "cloche metrics --workflow develop --step implement"
    Then the command succeeds
    And the output contains "implement"
    And the output contains "18,000"
    And the output contains "3,800"

  Scenario: cloche metrics --step filters to only that step
    Given the project has completed runs of the "develop" workflow
    And the "implement" step used 10000 input tokens and 2000 output tokens
    And the "test" step used 5000 input tokens and 1000 output tokens
    When the user runs "cloche metrics --workflow develop --step implement"
    Then the command succeeds
    And the output contains "implement"
    And the output does not contain "test"

  # ─── Aggregate by workflow ────────────────────────────────────────────────────

  Scenario: cloche metrics without --step shows all steps sorted by token total
    Given the project has completed runs of the "develop" workflow
    And the "implement" step used 100000 input tokens and 20000 output tokens
    And the "test" step used 10000 input tokens and 2000 output tokens
    When the user runs "cloche metrics --workflow develop"
    Then the command succeeds
    And the output contains "implement"
    And the output contains "test"
    And "implement" appears before "test" in the output

  # ─── Trend over time ─────────────────────────────────────────────────────────

  Scenario: cloche metrics --trend shows daily token breakdown for a step
    Given the project has completed runs of the "develop" workflow on multiple days
    And the "implement" step used 10000 tokens on "2026-05-22"
    And the "implement" step used 12000 tokens on "2026-05-23"
    When the user runs "cloche metrics --workflow develop --step implement --trend"
    Then the command succeeds
    And the output contains "2026-05-22"
    And the output contains "2026-05-23"

  # ─── Output formats ──────────────────────────────────────────────────────────

  Scenario: cloche metrics --format json outputs valid JSON
    Given the project has completed runs of the "develop" workflow
    And the "implement" step used 10000 input tokens and 2000 output tokens
    When the user runs "cloche metrics --workflow develop --step implement --format json"
    Then the command succeeds
    And the output is valid JSON
    And the JSON output contains a "total_tokens" field

  # ─── Time filtering ──────────────────────────────────────────────────────────

  Scenario: cloche metrics --since filters out older runs
    Given the project has completed runs of the "develop" workflow
    And the "implement" step used 10000 tokens on "2026-04-01"
    And the "implement" step used 5000 tokens on "2026-05-15"
    When the user runs "cloche metrics --workflow develop --step implement --since 2026-05-01"
    Then the command succeeds
    And the output shows a total of 5000 tokens
    And the output does not show a total of 15000 tokens
