Feature: token-limit config on steps and workflows

  A workflow author can set token-limit on a step or workflow block to cap the
  maximum output tokens consumed. Exceeding the limit aborts execution.

  # ─── L1: DSL parsing & validation ───────────────────────────────────────────

  Scenario: step-level token-limit is parsed into step config
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        step analyze {
          run = "echo analyze"
          results = [success]
          token-limit = 750000
        }
        analyze:success -> done
      }
      """
    When the token-limit DSL file is parsed
    Then no token-limit parse error is returned
    And step "analyze" in workflow "build" has a "token-limit" config value of "750000"

  Scenario: workflow-level token-limit is parsed into workflow config
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        token-limit = 1000000
        step analyze {
          run = "echo analyze"
          results = [success]
        }
        analyze:success -> done
      }
      """
    When the token-limit DSL file is parsed
    Then no token-limit parse error is returned
    And workflow "build" has a "token-limit" config value of "1000000"

  Scenario: every step gets an implicit token-limit result wired to abort
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        step analyze {
          run = "echo analyze"
          results = [success]
        }
        analyze:success -> done
      }
      """
    When the token-limit DSL file is parsed
    Then no token-limit parse error is returned
    And step "analyze" in workflow "build" has an implicit "token-limit" result wired to "abort"

  Scenario: an explicit token-limit wire in the workflow overrides the implicit one
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        step analyze {
          run = "echo analyze"
          results = [success]
        }
        step next-step {
          run = "echo continue"
          results = [success]
        }
        analyze:success -> done
        next-step:success -> done
        token-limit -> next-step
      }
      """
    When the token-limit DSL file is parsed
    Then no token-limit parse error is returned
    And in workflow "build" the wire from any step on "token-limit" goes to "next-step"

  Scenario: non-numeric token-limit value fails validation
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        step analyze {
          run = "echo analyze"
          results = [success]
          token-limit = abc
        }
        analyze:success -> done
      }
      """
    When the token-limit DSL file is parsed
    Then a token-limit parse error is returned
    And the token-limit error mentions "token-limit"

  Scenario: token-limit value less than -1 fails validation
    Given a token-limit DSL file containing:
      """
      workflow "build" {
        step analyze {
          run = "echo analyze"
          results = [success]
          token-limit = -2
        }
        analyze:success -> done
      }
      """
    When the token-limit DSL file is parsed
    Then a token-limit parse error is returned
    And the token-limit error mentions "token-limit"

  # ─── L2: Engine enforcement ──────────────────────────────────────────────────

  Scenario: step exceeding its output token limit yields token-limit result and aborts
    Given an engine with a single-step workflow where step "work" has token-limit 1000
    When step "work" completes reporting 1500 output tokens
    Then the engine step result for "work" is "token-limit"
    And the engine run is marked failed

  Scenario: workflow is aborted when cumulative output tokens cross the workflow limit
    Given an engine with a two-step workflow where the workflow has token-limit 3000
    When each step completes reporting 2000 output tokens
    Then the engine run is aborted after the first step
    And the engine run is marked failed

  Scenario: input tokens alone do not trigger an abort
    Given an engine with a single-step workflow where step "work" has token-limit 1000
    When step "work" completes reporting 500 output tokens and 50000 input tokens
    Then the engine step result for "work" is not "token-limit"
    And the engine run is not aborted

  Scenario: token-limit -1 disables per-step enforcement
    Given an engine with a single-step workflow where step "work" has token-limit -1
    When step "work" completes reporting 999999 output tokens
    Then the engine step result for "work" is not "token-limit"
    And the engine run is not aborted

  Scenario: token-limit 0 aborts a step immediately without calling the executor
    Given an engine with a single-step workflow where step "work" has token-limit 0
    When the engine executes the workflow
    Then the executor is never called for step "work"
    And the engine step result for "work" is "token-limit"
    And the engine run is marked failed

  Scenario: workflow-level token-limit 0 aborts before any step runs
    Given an engine with a single-step workflow where the workflow has token-limit 0
    When the engine executes the workflow
    Then no executor is called
    And the engine run is marked failed
