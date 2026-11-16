Feature: A DRA may mark tasks in its own domain ready (ruling CLOCHE-A160)
  A DRA may set the ready label on any task that carries its own domain label,
  on create and on update. The daily budget cap is the only limit. Tasks of
  another domain, or with no domain label, are still refused.

  Scenario: A DRA creates a task in its own domain as ready
    Given a DRA for the domain "games"
    When the DRA creates a task labelled "games" and "ready"
    Then the task is created
    And the task carries the "ready" label

  Scenario: A DRA marks an existing task in its own domain ready
    Given a DRA for the domain "games"
    And a task labelled "games" that is not ready
    When the DRA updates the task to add the "ready" label
    Then the update is accepted
    And the task carries the "ready" label

  Scenario: A DRA creates a task in its own domain without ready
    Given a DRA for the domain "games"
    When the DRA creates a task labelled "games"
    Then the task is created
    And the task does not carry the "ready" label

  Scenario: A DRA cannot create a ready task in another domain
    Given a DRA for the domain "games"
    When the DRA creates a task labelled "billing" and "ready"
    Then the request is refused
    And the error names the domain "games"
    And no task is created

  Scenario: A DRA cannot mark a task of another domain ready
    Given a DRA for the domain "games"
    And a task labelled "billing" that is not ready
    When the DRA updates the task to add the "ready" label
    Then the request is refused
    And the error names the domain "games"
    And the task does not carry the "ready" label

  Scenario: A DRA cannot create a ready task with no domain label
    Given a DRA for the domain "games"
    When the DRA creates a task with no domain label and the "ready" label
    Then the request is refused
    And the error names the domain "games"
    And no task is created

  Scenario: A DRA cannot mark a task with no domain label ready
    Given a DRA for the domain "games"
    And a task with no domain label that is not ready
    When the DRA updates the task to add the "ready" label
    Then the request is refused
    And the error names the domain "games"
    And the task does not carry the "ready" label

  Scenario: Adding the domain label and ready together on update is accepted
    Given a DRA for the domain "games"
    And a task with no domain label that is not ready
    When the DRA updates the task to add the "games" and "ready" labels
    Then the update is accepted
    And the task carries the "ready" label

  Scenario: The daily budget cap still applies to ready tasks
    Given a DRA for the domain "games"
    And the daily budget cap is exceeded
    When the DRA creates a task labelled "games" and "ready"
    Then the task is created
    And the loop does not start a run for the task

  Scenario: The DRA docs state the ruling instead of an open question
    When the DRA documentation is read
    Then it states that a DRA may mark any task in its own domain ready on create and update
    And it names ruling "CLOCHE-A160"
    And it no longer says a DRA cannot set ready
