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
	ctx.Step(`^the parsed file contains a repository named "([^"]*)" with path "([^"]*)"$`, pendingRepoWithPath)
	ctx.Step(`^the parsed file contains a repository named "([^"]*)" with url "([^"]*)"$`, pendingRepoWithURL)
	ctx.Step(`^the parsed file contains a repository named "([^"]*)" marked as default$`, pendingRepoIsDefault)
	ctx.Step(`^the parsed file contains a repository named "([^"]*)"$`, pendingRepoByName)
	ctx.Step(`^the parsed file contains (\d+) repositor(?:y|ies)$`, pendingRepoCount)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has repository "([^"]*)"$`, pendingStepHasRepository)

	// CLI project display (L1)
	ctx.Step(`^the project's \.cloche config declares:$`, pendingProjectConfigDeclares)
	ctx.Step(`^the project's \.cloche config has no repository blocks$`, pendingProjectConfigNoRepos)
	ctx.Step(`^the user runs "([^"]*)"$`, pendingUserRunsCommand)
	ctx.Step(`^the command succeeds$`, pendingCommandSucceeds)
	ctx.Step(`^the output contains "([^"]*)"$`, pendingOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, pendingOutputNotContains)

	// CLI repos subcommands (L2)
	ctx.Step(`^the project has a stored repository named "([^"]*)" with path "([^"]*)"$`, pendingStoredRepo)
	ctx.Step(`^the project has no stored repositories$`, pendingNoStoredRepos)

	// Backward compatibility (L2)
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
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingRepoWithPath(name, path string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingRepoWithURL(name, url string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingRepoIsDefault(name string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingRepoByName(name string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingRepoCount(count int) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

func pendingStepHasRepository(stepName, workflowName, repoName string) error {
	return errors.New("pending: L1 DSL parsing implementation")
}

// ─── CLI pending stubs (L1) ──────────────────────────────────────────────────

func pendingProjectConfigDeclares(config *godog.DocString) error {
	return errors.New("pending: L1 CLI surface implementation")
}

func pendingProjectConfigNoRepos() error {
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

// ─── CLI pending stubs (L2) ──────────────────────────────────────────────────

func pendingDaemonRunning() error {
	return errors.New("pending: L2 runtime/adapter implementation")
}

func pendingStoredRepo(name, path string) error {
	return errors.New("pending: L2 domain/persistence implementation")
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
