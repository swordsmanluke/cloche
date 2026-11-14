Feature: Group binds and bind settings
  A group bind such as `bind qa = saga` points every slot in a built-in group
  at one module. A bind may carry literal settings, `{ env.X = "..." }`, that
  reach the slot's run.

  # ── L1: parse, resolve, validate ──────────────────────────────────────────

  Scenario: Group bind qa resolves its three slots to one module
    Given a module "saga" exporting "ask", "await-answer" and "read-answer"
    And a workflow file containing "bind qa = saga"
    When the workflow file is parsed and slots are resolved
    Then slots "ask", "await-answer" and "read-answer" resolve to module "saga"

  Scenario: Group bind tasks equals the six-line form
    Given a module "beads-tasks" exporting all six tasks slots
    And a workflow file containing "bind tasks = beads-tasks"
    When the workflow file is parsed and slots are resolved
    Then the six tasks slots resolve to module "beads-tasks"
    And the resolution matches the six per-slot binds written out by hand

  Scenario: The six-line per-slot form still works
    Given a module "beads-tasks" exporting all six tasks slots
    And a workflow file with one per-slot bind for each of the six tasks slots
    When the workflow file is parsed and slots are resolved
    Then the six tasks slots resolve to module "beads-tasks"

  Scenario: A per-slot bind overrides a group bind in the same scope
    Given modules "saga" and "other" each exporting the qa slots
    And a workflow file containing "bind qa = saga" and "bind ask = other"
    When the workflow file is parsed and slots are resolved
    Then slot "ask" resolves to module "other"
    And slots "await-answer" and "read-answer" resolve to module "saga"

  Scenario: Existing .cloche files parse unchanged
    Given a workflow file using only per-slot binds and no settings blocks
    When the workflow file is parsed
    Then parsing succeeds with the same binds as before

  Scenario: Validate rejects a group bind whose module lacks an export
    Given a module "saga" exporting "ask" and "await-answer" but not "read-answer"
    And a workflow file containing "bind qa = saga"
    When the user runs "cloche validate"
    Then validation fails
    And the error names the missing export "read-answer"

  Scenario: Validate rejects a non-module group target and reserved workflow names
    Given a workflow file containing "bind qa = some-workflow" where the target is not a module
    When the user runs "cloche validate"
    Then validation fails with an error that the group bind target must be a module
    Given a workflow file defining a workflow named "qa"
    When the user runs "cloche validate"
    Then validation fails with an error that "qa" is a reserved group name

  Scenario: Validate warns when a bind sets a credential variable
    Given a module "saga" that lists "SAGA_TOKEN" as a credential
    And a workflow file containing a bind of "saga" with setting env.SAGA_TOKEN = "abc"
    When the user runs "cloche validate"
    Then validation succeeds
    And a warning names "SAGA_TOKEN" as a credential set on a bind

  # ── L2: bind env reaches slot runs ────────────────────────────────────────

  Scenario: Bind settings reach the slot's run on both run paths
    Given a stub module "saga" whose slots echo their environment
    And a workflow file containing "bind qa = saga { env.SAGA_PROJECT = "cloche" }"
    When the "ask" slot runs through the gRPC executor
    And the "ask" slot runs through the host phases
    Then both runs see SAGA_PROJECT equal to "cloche"

  Scenario: Precedence is daemon env, then outer bind, then inner bind, then the step's with block
    Given a stub module "saga" whose slots echo their environment
    And the daemon environment sets X to "daemon"
    And an outer-scope bind sets X to "outer" and an inner-scope bind sets X to "inner"
    When the slot runs
    Then the run sees X equal to "inner"
    When the step's with block sets X to "step"
    And the slot runs
    Then the run sees X equal to "step"

  Scenario: A bind setting satisfies a contract env requirement
    Given a stub module "saga" whose contract requires env "SAGA_PROJECT"
    And the daemon environment does not set SAGA_PROJECT
    And a workflow file containing "bind qa = saga { env.SAGA_PROJECT = "cloche" }"
    When the "ask" slot runs
    Then the run is not rejected for a missing env requirement
