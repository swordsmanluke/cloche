Feature: Migrate wrapper .cloche/prompts/ to {{ }} template directives

  Wrapper prompt files are migrated from legacy {previous_output} and
  $(clo get ...) surfaces to {{ $var }}, {{@ path }} directives with a
  standard header-block format. Scenarios here specify the observable
  contract at the prompt-assembly level.

  # ─── L1: vertical-fix.md ─────────────────────────────────────────────────────

  Scenario: vertical-fix.md surfaces test output inline via PREV_OUTPUT
    Given the wrapper prompt "vertical-fix.md" is loaded
    And the run context has prev_output "FAIL: TestMyFeature --- FAIL"
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "FAIL: TestMyFeature --- FAIL"
    And the assembled prompt does not contain "{previous_output}"

  Scenario: vertical-fix.md names the give-up file using TEMP_DIR
    Given the wrapper prompt "vertical-fix.md" is loaded
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "/tmp/cloche-test-abc/agent-give-up-reason.md"

  Scenario: vertical-fix.md contains no legacy placeholders after migration
    Given the wrapper prompt "vertical-fix.md" is loaded
    When the prompt file content is scanned for legacy patterns
    Then the prompt file does not contain the pattern "{previous_output}"
    And the prompt file does not contain the pattern "$(clo get"

  # ─── L2: five container-side prompts ─────────────────────────────────────────

  Scenario: vertical-implement.md inlines layer context without a cat instruction
    Given the wrapper prompt "vertical-implement.md" is loaded
    And the run context has layer_prompt_path "layer.md"
    And a scratch file "layer.md" contains "Implement the retry logic here"
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "Implement the retry logic here"
    And the assembled prompt does not contain "$(clo get layer_prompt_path)"

  Scenario: vertical-self-review.md substitutes current_base_branch
    Given the wrapper prompt "vertical-self-review.md" is loaded
    And the run context has current_base_branch "vertical/wrapped_cloche-7b2/wrapped_cloche-7b2.1"
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "vertical/wrapped_cloche-7b2/wrapped_cloche-7b2.1"
    And the assembled prompt does not contain "$(clo get current_base_branch)"

  Scenario: vertical-address-feedback.md substitutes feedback_path and base branch
    Given the wrapper prompt "vertical-address-feedback.md" is loaded
    And the run context has feedback_path "/tmp/cloche-test-abc/feedback.md"
    And the run context has current_base_branch "vertical/wrapped_cloche-7b2/wrapped_cloche-7b2.1"
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "/tmp/cloche-test-abc/feedback.md"
    And the assembled prompt does not contain "$(clo get feedback_path)"
    And the assembled prompt does not contain "$(clo get current_base_branch)"

  Scenario: vertical-update-docs.md substitutes vertical_base_branch
    Given the wrapper prompt "vertical-update-docs.md" is loaded
    And the run context has vertical_base_branch "feature/prompt-templating"
    And the run context has temp_file_dir "/tmp/cloche-test-abc"
    When the wrapper prompt is assembled
    Then the assembled prompt contains "feature/prompt-templating"
    And the assembled prompt does not contain "$(clo get vertical_base_branch"

  # ─── L2: claim-task.sh ───────────────────────────────────────────────────────

  Scenario: claim-task.sh seeds vertical_base_branch=main when the key is absent
    Given a fresh KV namespace without "vertical_base_branch"
    When claim-task.sh runs against that namespace
    Then the namespace has "vertical_base_branch" = "main"

  Scenario: claim-task.sh preserves an existing vertical_base_branch value
    Given a fresh KV namespace with "vertical_base_branch" = "feature/prompt-templating"
    When claim-task.sh runs against that namespace
    Then the namespace has "vertical_base_branch" = "feature/prompt-templating"
