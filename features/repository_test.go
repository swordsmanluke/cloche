package features_test

import (
	"errors"
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// ─── step registrations ──────────────────────────────────────────────────────

func initRepositoryScenarios(ctx *godog.ScenarioContext) {
	// Background (CLI feature)
	ctx.Step(`^the daemon is running against a test project directory$`, pendingDaemonRunning)

	// DSL parsing
	ctx.Step(`^a \.cloche file containing:$`, pendingClocheFileContaining)
	ctx.Step(`^the DSL parser processes the file$`, pendingDSLParserProcesses)
	ctx.Step(`^no parse error is returned$`, pendingNoParsedError)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has repository "([^"]*)"$`, pendingStepHasRepository)
	ctx.Step(`^workflow "([^"]*)" declares repos \[([^\]]*)\]$`, pendingWorkflowDeclaredRepos)
	ctx.Step(`^workflow "([^"]*)" declares (\d+) repos$`, pendingWorkflowRepoCount)

	// Config.toml parsing
	ctx.Step(`^a config\.toml containing:$`, pendingConfigTOMLContaining)
	ctx.Step(`^a config\.toml containing no repository entries$`, pendingConfigTOMLContainingNoRepos)
	ctx.Step(`^the config is parsed$`, pendingConfigParsed)
	ctx.Step(`^the config contains a repository named "([^"]*)" with path "([^"]*)"$`, pendingConfigContainsRepoWithPath)
	ctx.Step(`^the config contains a repository named "([^"]*)" marked as default$`, pendingConfigContainsRepoIsDefault)
	ctx.Step(`^the config contains a repository named "([^"]*)"$`, pendingConfigContainsRepoByName)
	ctx.Step(`^the config contains (\d+) repositor(?:y|ies)$`, pendingConfigContainsRepoCount)

	// CLI project display
	ctx.Step(`^the project's config\.toml declares:$`, pendingProjectConfigTOMLDeclares)
	ctx.Step(`^the project's config\.toml has no repository entries$`, pendingProjectConfigTOMLNoRepos)
	ctx.Step(`^the project's config\.toml has a repository entry named "([^"]*)" with path "([^"]*)"$`, pendingProjectConfigTOMLHasRepo)
	ctx.Step(`^the user runs "([^"]*)"$`, pendingUserRunsCommand)
	ctx.Step(`^the command succeeds$`, pendingCommandSucceeds)
	ctx.Step(`^the output contains "([^"]*)"$`, pendingOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, pendingOutputNotContains)
	ctx.Step(`^the output contains a deprecation warning about missing repository configuration$`, pendingOutputContainsDeprecationWarning)
	ctx.Step(`^the output contains migration instructions for adding repository configuration$`, pendingOutputContainsMigrationInstructions)

	// Backward compatibility
	ctx.Step(`^the project has no stored repositories$`, pendingNoStoredRepos)
	ctx.Step(`^a project database that has been freshly migrated with no repository rows$`, pendingFreshMigration)
	ctx.Step(`^the repositories store is first accessed for that project$`, pendingFirstAccess)
	ctx.Step(`^exactly (\d+) repositor(?:y|ies) (?:is|are) seeded automatically$`, pendingSeededCount)
	ctx.Step(`^the seeded repository is marked as default$`, pendingSeededIsDefault)
	ctx.Step(`^the seeded repository has path equal to the project root directory$`, pendingSeededPath)
}

// ─── DSL pending stubs (L1) ──────────────────────────────────────────────────

func pendingClocheFileContaining(content *godog.DocString) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingDSLParserProcesses() error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingNoParsedError() error {
	return errors.New("pending: L1 DSL/config parsing implementation")
}

func pendingStepHasRepository(stepName, workflowName, repoName string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingWorkflowDeclaredRepos(workflowName, reposList string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingWorkflowRepoCount(workflowName string, count int) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

// ─── Config.toml pending stubs (L1) ──────────────────────────────────────────

func pendingConfigTOMLContaining(content *godog.DocString) error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigTOMLContainingNoRepos() error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigParsed() error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigContainsRepoWithPath(name, path string) error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigContainsRepoIsDefault(name string) error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigContainsRepoByName(name string) error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

func pendingConfigContainsRepoCount(count int) error {
	return errors.New("pending: L1 config.toml parsing implementation")
}

// ─── CLI pending stubs (L1) ──────────────────────────────────────────────────

func pendingProjectConfigTOMLDeclares(config *godog.DocString) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingProjectConfigTOMLNoRepos() error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingProjectConfigTOMLHasRepo(name, path string) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingUserRunsCommand(cmd string) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingCommandSucceeds() error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingOutputContains(text string) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingOutputNotContains(text string) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingOutputContainsDeprecationWarning() error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingOutputContainsMigrationInstructions() error {
	return errors.New("pending: L1 CLI surface implementation")
}

// ─── CLI pending stubs (L2) ──────────────────────────────────────────────────

func pendingDaemonRunning() error {
	return errors.New("pending: L2 runtime/adapter implementation")
}

func pendingNoStoredRepos() error {
	return errors.New("pending: L2 domain/persistence implementation")
}

func pendingFreshMigration() error {
	return errors.New("pending: L2 domain/persistence implementation")
}

func pendingFirstAccess() error {
	return errors.New("pending: L2 domain/persistence implementation")
}

func pendingSeededCount(count int) error {
	return errors.New("pending: L2 domain/persistence implementation")
}

func pendingSeededIsDefault() error {
	return errors.New("pending: L2 domain/persistence implementation")
}

func pendingSeededPath() error {
	return errors.New("pending: L2 domain/persistence implementation")
}

// ─── TestMain ────────────────────────────────────────────────────────────────

func TestMain(m *testing.M) {
	opts := godog.Options{
		Format: "pretty",
		Paths:  []string{"."},
	}

	status := godog.TestSuite{
		Name:                "repository",
		ScenarioInitializer: initRepositoryScenarios,
		Options:             &opts,
	}.Run()

	if st := m.Run(); st > status {
		status = st
	}
	os.Exit(status)
}
