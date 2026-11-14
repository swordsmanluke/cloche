Feature: Group binds and bind settings
  A group bind (`bind qa = saga`) routes every slot in a built-in group to one
  module's same-named export. A bind may carry literal env settings
  (`bind qa = saga { env.X = "..." }`) that reach the slot's run.

  Background:
    Given a gbind project with a module "saga" exporting ask, await-answer and read-answer

  # ── L1: parse, resolve, validate ──────────────────────────────────────────

  Scenario: bind qa resolves all three qa slots to the module
    Given a gbind workflow containing "bind qa = saga"
    When the gbind user runs "cloche validate"
    Then the gbind validation succeeds
    And the gbind slots "ask, await-answer, read-answer" resolve to module "saga"

  Scenario: bind tasks equals the six-line form, which still works
    Given a gbind module "beads-tasks" exporting list-tasks, claim-task, close-task, release-task, create-task and update-task
    And a gbind workflow containing "bind tasks = beads-tasks"
    And a second gbind workflow with six per-slot binds to "beads-tasks"
    When the gbind user resolves the six task slots in both workflows
    Then both workflows resolve every task slot to "beads-tasks"

  Scenario: a per-slot bind overrides a group bind in the same scope
    Given a gbind module "other" exporting ask
    And a gbind workflow containing "bind qa = saga" and "bind ask = other"
    When the gbind user resolves the qa slots
    Then slot "ask" resolves to module "other"
    And slots "await-answer, read-answer" resolve to module "saga"

  Scenario: validate names the missing export of a group bind
    Given a gbind module "partial" exporting ask only
    And a gbind workflow containing "bind qa = partial"
    When the gbind user runs "cloche validate"
    Then the gbind validation fails with an error naming "await-answer"

  Scenario: validate rejects a group bind whose target is not a module
    Given a gbind workflow containing "bind qa = some-workflow"
    When the gbind user runs "cloche validate"
    Then the gbind validation fails with an error saying the target must be a module

  Scenario Outline: group names are reserved as workflow names
    Given a gbind workflow named "<name>"
    When the gbind user runs "cloche validate"
    Then the gbind validation fails with an error saying "<name>" is reserved

    Examples:
      | name  |
      | qa    |
      | tasks |

  Scenario: validate warns when a bind sets a credential variable
    Given the gbind module "saga" lists SAGA_TOKEN as a credential
    And a gbind workflow containing 'bind qa = saga { env.SAGA_TOKEN = "x" }'
    When the gbind user runs "cloche validate"
    Then the gbind validation succeeds with a warning naming "SAGA_TOKEN"

  Scenario: existing .cloche files parse unchanged
    Given the gbind repository's existing .cloche files
    When the gbind user runs "cloche validate"
    Then the gbind validation succeeds with no new errors

  # ── L2: bind env reaches slot runs ────────────────────────────────────────

  Scenario Outline: bind env reaches the slot's run on both run paths
    Given a gbind workflow containing 'bind qa = saga { env.SAGA_PROJECT = "cloche" }'
    When the gbind slot "ask" runs via the "<path>" path
    Then the run sees SAGA_PROJECT equal to "cloche"

    Examples:
      | path          |
      | grpc executor |
      | host phases   |

  Scenario: precedence is daemon env, then outer bind, then inner bind, then step with
    Given the daemon environment sets X to "daemon"
    And an outer-scope bind sets env.X to "outer"
    And an inner-scope bind sets env.X to "inner"
    When the gbind slot runs with a step with-block setting X to "step"
    Then the run sees X equal to "step"
    When the gbind slot runs without a with-block
    Then the run sees X equal to "inner"

  Scenario: group bind settings apply to every slot in the group
    Given a gbind workflow containing 'bind qa = saga { env.SAGA_PROJECT = "cloche" }'
    When the gbind user runs each of ask, await-answer and read-answer
    Then every run sees SAGA_PROJECT equal to "cloche"

  Scenario: a bind setting satisfies a contract env requirement
    Given the gbind module "saga" contract requires env SAGA_PROJECT
    And a gbind workflow containing 'bind qa = saga { env.SAGA_PROJECT = "cloche" }'
    And SAGA_PROJECT is not set in the daemon environment
    When the gbind slot "ask" runs
    Then the run is not rejected for a missing env requirement
