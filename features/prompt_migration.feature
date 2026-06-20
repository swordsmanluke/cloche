Feature: Migrate .cloche/prompts/ to {{ }} templating and header-block format
  As a cloche developer
  I want prompt files to use {{ $var }} directives instead of $(clo get ...) expressions
  So that prompts are resolved by the template engine before reaching the LLM

  # ─── L1: vertical-fix.md ─────────────────────────────────────────────────────

  Scenario: vertical-fix.md has no legacy shell substitutions
    Given the cloche prompt file "vertical-fix.md"
    Then the prompt file does not contain "$(clo get"

  Scenario: vertical-fix.md opens with a header block declaring PREV_OUTPUT and TEMP_DIR
    Given the cloche prompt file "vertical-fix.md"
    Then the prompt file opens with a header block
    And the header block contains "PREV_OUTPUT"
    And the header block contains "TEMP_DIR"

  Scenario: vertical-fix.md still references the agent give-up reason file
    Given the cloche prompt file "vertical-fix.md"
    Then the prompt file mentions "agent-give-up-reason.md"

  # ─── L2: container-side vertical prompts ─────────────────────────────────────

  Scenario: Container-side vertical prompts have no legacy shell substitutions
    Given the container-side vertical prompt files
    Then none of the prompt files contain "$(clo get"

  Scenario: Container-side vertical prompts all open with header blocks
    Given the container-side vertical prompt files
    Then all prompt files open with a header block

  Scenario: vertical-implement.md uses inline layer context embed instead of cat instruction
    Given the cloche prompt file "vertical-implement.md"
    Then the prompt file contains "{{@"
    And the prompt file does not contain "cat $(clo get layer_prompt_path)"

  Scenario: vertical-implement.md header block declares LAYER_CONTEXT
    Given the cloche prompt file "vertical-implement.md"
    Then the prompt file opens with a header block
    And the header block contains "LAYER_CONTEXT"

  Scenario: claim-task.sh seeds vertical_base_branch when unset
    Given the cloche script file "claim-task.sh"
    Then the script file contains "vertical_base_branch"
