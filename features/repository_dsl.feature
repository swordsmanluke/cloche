Feature: Repository configuration and DSL
  As a developer using Cloche
  I want to declare repositories in the project config and reference them from workflows
  So that workflows can access the correct git repositories

  # ─── Project config.toml parsing ─────────────────────────────────────────────

  Scenario: Parse a single repository entry from config.toml
    Given a config.toml containing:
      """
      [[repositories]]
      name = "backend"
      path = "./repos/backend"
      """
    When the config is parsed
    Then no parse error is returned
    And the config contains a repository named "backend" with path "./repos/backend"
    And the single-entry config has an implicit default repository named "backend"

  Scenario: Parse multiple repository entries from config.toml
    Given a config.toml containing:
      """
      [[repositories]]
      name = "backend"
      path = "./repos/backend"

      [[repositories]]
      name = "frontend"
      path = "./repos/frontend"
      """
    When the config is parsed
    Then no parse error is returned
    And the config contains a repository named "backend"
    And the config contains a repository named "frontend"
    And the config contains 2 repositories

  Scenario: A config.toml with no repository entries parses without error
    Given a config.toml containing no repository entries
    When the config is parsed
    Then no parse error is returned
    And the config contains 0 repositories

  # ─── Workflow DSL repo declarations ──────────────────────────────────────────

  Scenario: Parse a workflow with a repos declaration
    Given a .cloche file containing:
      """
      workflow "develop-backend" {
        repos = ["backend"]
        step build {
          run     = "make build"
          results = [success, fail]
        }
        build:success -> done
        build:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And workflow "develop-backend" declares repos ["backend"]

  Scenario: Parse a workflow with multiple repo dependencies
    Given a .cloche file containing:
      """
      workflow "integration-testing" {
        repos = ["candy", "cloche"]
        step test {
          run     = "make test"
          results = [success, fail]
        }
        test:success -> done
        test:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And workflow "integration-testing" declares repos ["candy", "cloche"]

  Scenario: Parse a step with a repository field
    Given a .cloche file containing:
      """
      workflow "deploy" {
        repos = ["backend"]
        step build {
          run        = "make build"
          repository = "backend"
          results    = [success, fail]
        }
        build:success -> done
        build:fail    -> abort
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And step "build" in workflow "deploy" has repository "backend"

  Scenario: A workflow without a repos declaration parses without error
    Given a .cloche file containing:
      """
      workflow "develop" {
        step run {
          run     = "echo hi"
          results = [success]
        }
        run:success -> done
      }
      """
    When the DSL parser processes the file
    Then no parse error is returned
    And workflow "develop" declares 0 repos
