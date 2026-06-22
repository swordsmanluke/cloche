Feature: Resume rebuild and preserve workspace

  When a run fails, "cloche resume" should rebuild the container from the
  project Dockerfile (picking up any image fixes) and re-apply the workspace
  changes from the last successful step, so progress is never discarded and
  Dockerfile bugs are fixed on retry.

  # ── L1: Snapshot capture ─────────────────────────────────────────────────

  Scenario: Workspace snapshot is captured after each successful step
    Given a cloche run is executing in a container
    When a step completes successfully
    Then a workspace snapshot is saved for that step under the run record
    And the snapshot includes modified and new files relative to the initial image

  # ── L2: CLI flags accepted ────────────────────────────────────────────────

  Scenario: cloche resume accepts --no-rebuild flag
    Given a task has a prior failed run with at least one successful step
    When the user runs "cloche resume --no-rebuild" for that task
    Then the command is accepted without error

  Scenario: cloche resume accepts --clean flag
    Given a task has a prior failed run with at least one successful step
    When the user runs "cloche resume --clean" for that task
    Then the command is accepted without error

  # ── L3: Rebuild behavior ──────────────────────────────────────────────────

  Scenario: Default resume rebuilds the container and re-applies the workspace snapshot
    Given a task has a prior failed run with a workspace snapshot after step "fetch-deps"
    When the user runs "cloche resume" for that task
    Then the daemon builds a fresh container from the project Dockerfile
    And the workspace snapshot from step "fetch-deps" is applied to the container
    And the failed step is retried inside the fresh container

  Scenario: Resume with --no-rebuild reuses the existing container image
    Given a task has a prior failed run with a workspace snapshot after step "build"
    When the user runs "cloche resume --no-rebuild" for that task
    Then the daemon does not rebuild the container
    And the workspace snapshot from step "build" is applied before retry

  Scenario: Resume with --clean rebuilds the container but skips the snapshot
    Given a task has a prior failed run with a workspace snapshot after step "lint"
    When the user runs "cloche resume --clean" for that task
    Then the daemon builds a fresh container from the project Dockerfile
    And no workspace snapshot is applied to the container

  # ── L3: Multi-attempt selection ───────────────────────────────────────────

  Scenario: Default resume selects the most recent attempt with a successful step
    Given a task has two prior attempts
    And the first attempt has completed step "install" successfully
    And the second attempt has no successful steps
    When the user runs "cloche resume" for that task
    Then the daemon uses the workspace snapshot from the first attempt

  Scenario: Resume with --attempt selects a specific prior attempt
    Given a task has two prior attempts each with at least one successful step
    When the user runs "cloche resume --attempt <first-run-id>" for that task
    Then the daemon uses the workspace snapshot from that specified attempt

  Scenario: Resume with no prior successful steps restarts from the beginning
    Given a task has a prior attempt with no successful steps
    When the user runs "cloche resume" for that task
    Then no workspace snapshot is applied to the container
    And the run starts from the first step

  # ── L3: Conflict resolution ───────────────────────────────────────────────

  Scenario: Agent writes take precedence over Dockerfile changes on conflict
    Given a workspace snapshot contains changes to a file that the rebuilt image also modifies
    When the snapshot is applied to the fresh container
    Then the file in the container reflects the agent's version

  Scenario: Unresolvable merge conflict fails the run before the agent is invoked
    Given a workspace snapshot has an unresolvable conflict with a file in the rebuilt image
    When the user runs "cloche resume" for that task
    Then the run fails before the agent step is dispatched
    And the error output names the conflicting file
