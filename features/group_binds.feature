Feature: Group binds and bind settings
  A group bind (`bind qa = saga`, `bind tasks = beads-tasks`) binds every slot
  in a built-in group to the same-named export of one module. A bind may carry
  literal settings (`{ env.X = "..." }`) that reach the slot's run.

  # ── L1: parse, resolve, validate ──────────────────────────────────────────

  Scenario: bind qa resolves ask, await-answer and read-answer to the module
    Given a module "saga" exporting ask, await-answer and read-answer
    And a workflow containing "bind qa = saga"
    When the workflow is resolved
    Then slots "ask", "await-answer" and "read-answer" resolve to module "saga"

  Scenario: bind tasks equals the six-line per-slot form
    Given a module "beads-tasks" exporting all six tasks slots
    And a workflow containing "bind tasks = beads-tasks"
    When the workflow is resolved
    Then the six tasks slots resolve to module "beads-tasks"
    And the result equals resolving the six-line per-slot form

  Scenario: A per-slot bind overrides a group bind in the same scope
    Given a module "saga" exporting ask, await-answer and read-answer
    And a module "other" exporting ask
    And a workflow containing "bind qa = saga"
    And the same scope also contains the per-slot bind "bind ask = other"
    When the workflow is resolved
    Then slot "ask" resolves to module "other"
    And slot "await-answer" resolves to module "saga"

  Scenario: Existing .cloche files without group binds parse unchanged
    Given an existing workflow file using only per-slot binds
    When the workflow is parsed
    Then parsing succeeds
    And its binds are unchanged

  Scenario: validate rejects a group bind whose module lacks an export
    Given a module "saga" exporting ask and await-answer but not read-answer
    And a workflow containing "bind qa = saga"
    When the user runs "cloche validate"
    Then validation fails with an error naming "read-answer"

  Scenario: validate rejects a group bind targeting a non-module
    Given a workflow containing "bind qa = some-workflow" where the target is not a module
    When the user runs "cloche validate"
    Then validation fails with an error saying the target must be a module

  Scenario: validate rejects a workflow named after a reserved group
    Given a workflow named "qa"
    When the user runs "cloche validate"
    Then validation fails with an error saying "qa" is a reserved group name

  Scenario: validate warns when a bind sets a credential variable
    Given a module "saga" that lists SAGA_TOKEN as a credential
    And a workflow containing "bind qa = saga { env.SAGA_TOKEN = \"x\" }"
    When the user runs "cloche validate"
    Then validation succeeds with a warning naming "SAGA_TOKEN"

  # ── L2: bind env reaches slot runs ────────────────────────────────────────

  Scenario: Bind settings reach the slot's run
    Given a stub module "saga" whose slots echo their environment
    And a workflow containing "bind qa = saga { env.SAGA_PROJECT = \"cloche\" }"
    When a step runs the "ask" slot
    Then the slot's environment has SAGA_PROJECT = "cloche"

  Scenario: Precedence is daemon env, then bind settings, then the step's with block
    Given a stub module "saga" whose slots echo their environment
    And the daemon environment sets SAGA_PROJECT to "daemon"
    And an outer-scope bind sets SAGA_PROJECT to "outer"
    And an inner-scope bind sets SAGA_PROJECT to "inner"
    When a step runs the "ask" slot
    Then the slot's environment has SAGA_PROJECT = "inner"
    When a step runs the "ask" slot with a with block setting SAGA_PROJECT to "step"
    Then the slot's environment has SAGA_PROJECT = "step"

  Scenario: A bind setting satisfies a contract env requirement
    Given a stub module "saga" whose contract requires env SAGA_PROJECT
    And a workflow containing "bind qa = saga { env.SAGA_PROJECT = \"cloche\" }"
    When a step runs the "ask" slot on both the daemon and host paths
    Then the run is not rejected for a missing SAGA_PROJECT
