Feature: Repository CLI surface
  As a developer using Cloche
  I want to view and manage project repositories via the cloche CLI
  So that I can configure multi-repo projects without editing config files by hand

  Background:
    Given the daemon is running against a test project directory

  # ─── cloche project display (L1) ────────────────────────────────────────────

  Scenario: cloche project shows a Repositories section when repositories are declared
    Given the project's .cloche config declares:
      """
      repository "backend" {
        path    = "../backend"
        url     = "https://github.com/org/backend"
        default = true
      }
      repository "frontend" {
        path = "../frontend"
      }
      """
    When the user runs "cloche project"
    Then the command succeeds
    And the output contains "Repositories:"
    And the output contains "backend"
    And the output contains "../backend"
    And the output contains "https://github.com/org/backend"
    And the output contains "frontend"

  Scenario: cloche project shows no Repositories section for a legacy single-repo project
    Given the project's .cloche config has no repository blocks
    When the user runs "cloche project"
    Then the command succeeds
    And the output does not contain "Repositories:"

  # ─── cloche project repos subcommands (L2) ──────────────────────────────────

  Scenario: cloche project repos list shows persisted repositories
    Given the project has a stored repository named "backend" with path "../backend"
    When the user runs "cloche project repos list"
    Then the command succeeds
    And the output contains "backend"
    And the output contains "../backend"

  Scenario: cloche project repos add persists a new repository
    Given the project has no stored repositories
    When the user runs "cloche project repos add --name backend --path ../backend --url https://github.com/org/backend --default"
    Then the command succeeds
    When the user runs "cloche project repos list"
    Then the command succeeds
    And the output contains "backend"
    And the output contains "../backend"
    And the output contains "https://github.com/org/backend"

  Scenario: cloche project repos remove deletes a repository
    Given the project has a stored repository named "backend" with path "../backend"
    When the user runs "cloche project repos remove --name backend"
    Then the command succeeds
    When the user runs "cloche project repos list"
    Then the command succeeds
    And the output does not contain "backend"

  Scenario: cloche project shows Repositories from the DB
    Given the project has a stored repository named "backend" with path "../backend"
    And the project has a stored repository named "frontend" with path "../frontend"
    When the user runs "cloche project"
    Then the command succeeds
    And the output contains "Repositories:"
    And the output contains "backend"
    And the output contains "frontend"

  # ─── Backward compatibility ──────────────────────────────────────────────────

  Scenario: Existing single-repo project works without config changes
    Given the project's .cloche config has no repository blocks
    And the project has no stored repositories
    When the user runs "cloche project"
    Then the command succeeds
    And the output contains "Project:"
    And the output contains "Directory:"

  Scenario: Upgrading the database auto-seeds a default repository from the project root
    Given a project database that has been freshly migrated with no repository rows
    When the repositories store is first accessed for that project
    Then exactly 1 repository is seeded automatically
    And the seeded repository is marked as default
    And the seeded repository has path equal to the project root directory
