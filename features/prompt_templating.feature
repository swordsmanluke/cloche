Feature: Prompt template DSL with {{ }} directives
  As a workflow author
  I want to inject KV values, built-in metadata, file contents, and shell output into prompts
  So that steps receive pre-computed data deterministically, before the LLM is invoked

  Background:
    Given a test resolver with built-ins and an empty KV store

  # ── Happy path: variable directive ──────────────────────────────────────────

  Scenario: Variable directive resolves a KV value
    Given the KV store contains "release_branch" = "main"
    When a prompt template "Deploy to {{ $release_branch }}" is resolved
    Then the resolved prompt is "Deploy to main"

  Scenario: Built-in variable provides run metadata
    Given the built-ins contain "task_id" = "feat-42"
    When a prompt template "Task is {{ $task_id }}" is resolved
    Then the resolved prompt is "Task is feat-42"

  Scenario: Built-in shadows KV value with the same name
    Given the KV store contains "task_id" = "kv-override"
    And the built-ins contain "task_id" = "builtin-42"
    When a prompt template "{{ $task_id }}" is resolved
    Then the resolved prompt is "builtin-42"

  # ── Happy path: file and shell directives ───────────────────────────────────

  Scenario: File directive reads a file into the prompt
    Given a file "data.txt" containing "hello world"
    When a prompt template "Content: {{@ data.txt }}" is resolved
    Then the resolved prompt is "Content: hello world"

  Scenario: Shell directive captures stdout into the prompt
    Given the shell timeout is 30s
    When a prompt template "Branch: {{! echo feature-x }}" is resolved
    Then the resolved prompt is "Branch: feature-x"

  # ── Nesting and escaping ────────────────────────────────────────────────────

  Scenario: Nested variable inside file directive
    Given the KV store contains "file_name" = "data.txt"
    And a file "data.txt" containing "nested content"
    When a prompt template "{{@ {{ $file_name }} }}" is resolved
    Then the resolved prompt is "nested content"

  Scenario: Nested variable inside shell directive
    Given the built-ins contain "run_id" = "run-99"
    When a prompt template "{{! echo run={{ $run_id }} }}" is resolved
    Then the resolved prompt is "run=run-99"

  Scenario: Escaped dollar inside shell directive
    When a prompt template "{{! echo $$HOME is set }}" is resolved
    Then the resolved prompt contains "$HOME is set"

  Scenario: Double-dollar is untouched outside shell directive
    When a prompt template "Price is $$50" is resolved
    Then the resolved prompt is "Price is $$50"

  Scenario: Whitespace inside braces is tolerated
    When a prompt template "{{  $task_id  }}" is resolved
    Then the resolved prompt is "builtin-42"

  # ── Strict error modes ──────────────────────────────────────────────────────

  Scenario: Missing variable fails the step before the LLM
    When a prompt template "{{ $nonexistent }}" is resolved
    Then resolution fails with error containing "variable not defined"

  Scenario: Missing file fails the step before the LLM
    When a prompt template "{{@ missing.txt }}" is resolved
    Then resolution fails with error containing "no such file"

  Scenario: Non-zero shell exit fails the step before the LLM
    When a prompt template "{{! false }}" is resolved
    Then resolution fails with error containing "exit status"

  Scenario: Shell timeout fails the step before the LLM
    Given the shell timeout is 1s
    When a prompt template "{{! sleep 5 }}" is resolved
    Then resolution fails with error containing "timeout"

  # ── No re-templating of injected content ────────────────────────────────────

  Scenario: File contents containing template syntax are not re-evaluated
    Given a file "raw.txt" containing "literal {{ $x }} here"
    When a prompt template "{{@ raw.txt }}" is resolved
    Then the resolved prompt is "literal {{ $x }} here"

  Scenario: Shell output containing template syntax are not re-evaluated
    When a prompt template "{{! echo 'output {{ $y }}' }}" is resolved
    Then the resolved prompt is "output {{ $y }}"

  # ── Legacy single-brace placeholders ────────────────────────────────────────

  Scenario: Legacy task_description still substitutes
    When a prompt template "Desc: {task_description}" is resolved with user prompt "Fix bug"
    Then the resolved prompt is "Desc: Fix bug"
    And exactly one deprecation warning is emitted for "{task_description}"

  Scenario: Legacy previous_output still substitutes
    When a prompt template "Prev: {previous_output}" is resolved with previous output "done"
    Then the resolved prompt is "Prev: done"
    And exactly one deprecation warning is emitted for "{previous_output}"

  Scenario: Mixed legacy and new syntax for the same value
    When a prompt template "A: {{ $task_description }} B: {task_description}" is resolved with user prompt "Do X"
    Then the resolved prompt is "A: Do X B: Do X"
    And exactly one deprecation warning is emitted for "{task_description}"

  # ── KV wiring integration ───────────────────────────────────────────────────

  @integration
  Scenario: Host step resolves KV value written by a preceding step
    Given the daemon is running against a test project directory
    And a KV key "db_host" has been set to "postgres.local"
    When a host step prompt "Connect to {{ $db_host }}" is resolved
    Then the resolved prompt is "Connect to postgres.local"

  @integration
  Scenario: Container step resolves KV value via gRPC
    Given the daemon is running against a test project directory
    And a container step sets KV key "env" to "staging"
    When a container step prompt "Deploy to {{ $env }}" is resolved
    Then the resolved prompt is "Deploy to staging"
