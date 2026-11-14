Feature: Missing-marker recovery turn constrains the agent to the step's declared results
  When an agent finishes a step without printing a result marker, the daemon
  runs a recovery turn asking for one. The recovery turn must tell the agent
  which result names are valid, and a bad answer must not throw away the
  finished work by aborting the workflow.

  # ── L1: recovery prompt content ────────────────────────────────────────────

  Scenario: Recovery prompt lists every declared result name
    Given a step that declares the results "success" and "fail"
    When the recovery prompt is built for that step
    Then the recovery prompt names the result "success"
    And the recovery prompt names the result "fail"

  Scenario: Recovery prompt says no other result name is valid
    Given a step that declares the results "success" and "fail"
    When the recovery prompt is built for that step
    Then the recovery prompt states that only the declared results are valid

  Scenario: Recovery prompt reflects a different set of declared results
    Given a step that declares the results "approved", "rejected" and "needs-work"
    When the recovery prompt is built for that step
    Then the recovery prompt names the result "approved"
    And the recovery prompt names the result "rejected"
    And the recovery prompt names the result "needs-work"
    And the recovery prompt does not name the result "success"

  Scenario: Recovery prompt still asks for the nonce-qualified marker
    Given a step that declares the results "success" and "fail"
    When the recovery prompt is built for that step
    Then the recovery prompt asks for a CLOCHE_RESULT marker carrying the run nonce

  # ── L2: recovery answer handling ───────────────────────────────────────────

  Scenario: Valid recovery answer passes through unchanged
    Given a step that declares the results "success" and "fail"
    And the agent finishes the step without printing a result marker
    When the recovery turn answers with the result "success"
    Then the step result is "success"
    And the run does not abort

  Scenario: Undeclared recovery answer takes the fail wire instead of aborting
    Given a step that declares the results "success" and "fail"
    And the agent finishes the step without printing a result marker
    When the recovery turn answers with the result "done"
    Then the run does not abort
    And the step result is "fail"
    And the step's fail wire is taken

  Scenario: Undeclared recovery answer does not discard the agent's finished work
    Given a step that declares the results "success" and "fail"
    And the agent finishes the step without printing a result marker
    When the recovery turn answers with the result "done"
    Then the sub-workflow is not reported as failed with "undeclared result"

  Scenario: Recovery uses the step's configured results end to end
    Given a workflow whose implement step declares the results "success" and "fail"
    And the agent finishes the implement step without printing a result marker
    When the recovery turn answers with the result "done"
    Then the implement step's fail wire is taken
    And the workflow continues past the implement step
