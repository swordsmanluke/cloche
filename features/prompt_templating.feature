Feature: Prompt template DSL with {{ }} directives
  As a workflow author
  I want to inject KV-store values, built-in metadata, file contents, and shell output into prompt templates
  So that steps receive data cloche already knows without burning LLM tokens to re-discover it

  Background:
    Given a clean template workspace

  # ─── Variable directive: {{ $name }} ─────────────────────────────────────────

  Scenario: Built-in variable resolves in a prompt template
    Given a prompt template "Task is {{ $task_id }}"
    And the run has task_id "abc-123"
    When the template is resolved
    Then the resolved prompt contains "Task is abc-123"

  Scenario: KV-store variable resolves when no built-in matches
    Given a prompt template "Read {{ $artifact_path }} for results"
    And the KV store has "artifact_path" = "/tmp/output.tar.gz"
    When the template is resolved
    Then the resolved prompt contains "Read /tmp/output.tar.gz for results"

  Scenario: Built-in shadows a KV write of the same name
    Given a prompt template "ID is {{ $task_id }}"
    And the KV store has "task_id" = "kv-override"
    And the run has task_id "real-task-id"
    When the template is resolved
    Then the resolved prompt contains "ID is real-task-id"
    And the resolved prompt does not contain "kv-override"

  Scenario: Missing variable fails the step before the agent runs
    Given a prompt template "{{ $no_such_var }}"
    And the KV store is empty
    When the template is resolved
    Then resolution fails with an error mentioning "no_such_var"

  # ─── Shell directive: {{! cmd }} ─────────────────────────────────────────────

  Scenario: Shell directive substitutes command stdout into the prompt
    Given a prompt template "{{! echo greetings }}"
    When the template is resolved
    Then the resolved prompt contains "greetings"

  Scenario: Inner variable resolves inside shell directive before the command runs
    Given a prompt template "{{! echo {{ $step_name }} }}"
    And the run has step_name "analyze"
    When the template is resolved
    Then the resolved prompt contains "analyze"

  Scenario: $$ inside shell directive becomes a literal $ for the shell
    Given a prompt template "{{! echo $$CLOCHE_BDD_TEST_VAR }}"
    And "CLOCHE_BDD_TEST_VAR" is an env var with the value "hello-cloche"
    When the template is resolved
    Then the resolved prompt contains "hello-cloche"

  Scenario: Shell directive fails when command exits non-zero
    Given a prompt template "{{! exit 42 }}"
    When the template is resolved
    Then resolution fails with an error mentioning exit status

  # ─── File directive: {{@ path }} ─────────────────────────────────────────────

  Scenario: File directive substitutes file contents into the prompt
    Given a file "context.txt" in the workdir containing "file contents here"
    And a prompt template "{{@ context.txt }}"
    When the template is resolved
    Then the resolved prompt contains "file contents here"

  Scenario: Inner variable in file path resolves before the file is read
    Given a file "analyze.txt" in the workdir containing "step context loaded"
    And a prompt template "{{@ {{ $step_name }}.txt }}"
    And the run has step_name "analyze"
    When the template is resolved
    Then the resolved prompt contains "step context loaded"

  Scenario: File directive fails when the referenced file does not exist
    Given a prompt template "{{@ missing_file.txt }}"
    When the template is resolved
    Then resolution fails with an error mentioning "missing_file.txt"

  Scenario: File contents containing template syntax are not re-templated
    Given a file "raw.txt" in the workdir containing "{{ $task_id }}"
    And a prompt template "{{@ raw.txt }}"
    When the template is resolved
    Then the resolved prompt contains "{{ $task_id }}"

  # ─── Legacy compatibility ─────────────────────────────────────────────────────

  Scenario: Legacy {task_description} placeholder still substitutes
    Given a prompt template "{task_description} is the goal"
    And the run has task_description "implement the feature"
    When the template is resolved with legacy support
    Then the resolved prompt contains "implement the feature is the goal"
    And a deprecation warning is emitted for "{task_description}"

  Scenario: Legacy {previous_output} placeholder still substitutes
    Given a prompt template "Prior: {previous_output}"
    And the run has previous_output "step 1 produced this"
    When the template is resolved with legacy support
    Then the resolved prompt contains "Prior: step 1 produced this"
    And a deprecation warning is emitted for "{previous_output}"

  Scenario: Deprecation warning is emitted only once per step per pattern
    Given a prompt template "{task_description} and again {task_description}"
    And the run has task_description "do the thing"
    When the template is resolved with legacy support
    Then exactly 1 deprecation warning is emitted for "{task_description}"

  # ─── KV integration end-to-end (L2) ──────────────────────────────────────────

  Scenario: Prompt uses a KV value set by a previous step in the same run
    Given the daemon is running with a test KV store
    And the KV store has "output_file" = "/workspace/result.json"
    And a prompt template "Read {{ $output_file }} for the results"
    When the template is resolved using the real KV reader
    Then the resolved prompt contains "/workspace/result.json"
