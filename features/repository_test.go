package features_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
)

// repositoryContext is the per-scenario state carried between Given/When/Then
// steps. godog gives every scenario its own ScenarioContext, so a fresh value
// is created via Before hooks below.
type repositoryContext struct {
	// Config.toml parsing
	configSource string
	parsedConfig *config.Config
	parseErr     error

	// DSL parsing
	clocheSource    string
	parsedWorkflows map[string]*domain.Workflow
	dslErr          error
}

func newRepositoryContext() *repositoryContext { return &repositoryContext{} }

// ─── step registrations ──────────────────────────────────────────────────────

func initRepositoryScenarios(ctx *godog.ScenarioContext) {
	rc := newRepositoryContext()
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*rc = repositoryContext{}
		return c, nil
	})

	// Background (CLI feature) — still pending until daemon-backed plumbing lands.
	ctx.Step(`^the daemon is running against a test project directory$`, pendingDaemonRunning)

	// DSL parsing
	ctx.Step(`^a \.cloche file containing:$`, rc.clocheFileContaining)
	ctx.Step(`^the DSL parser processes the file$`, rc.dslParserProcesses)
	ctx.Step(`^no parse error is returned$`, rc.noParseError)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has repository "([^"]*)"$`, rc.stepHasRepository)
	ctx.Step(`^workflow "([^"]*)" declares repos \[([^\]]*)\]$`, rc.workflowDeclaresRepos)
	ctx.Step(`^workflow "([^"]*)" declares (\d+) repos$`, rc.workflowRepoCount)

	// Config.toml parsing
	ctx.Step(`^a config\.toml containing:$`, rc.configTOMLContaining)
	ctx.Step(`^a config\.toml containing no repository entries$`, rc.configTOMLContainingNoRepos)
	ctx.Step(`^the config is parsed$`, rc.configIsParsed)
	ctx.Step(`^the config contains a repository named "([^"]*)" with path "([^"]*)"$`, rc.configContainsRepoWithPath)
	ctx.Step(`^the config contains a repository named "([^"]*)" marked as default$`, rc.configContainsRepoIsDefault)
	ctx.Step(`^the config contains a repository named "([^"]*)"$`, rc.configContainsRepoByName)
	ctx.Step(`^the config contains (\d+) repositor(?:y|ies)$`, rc.configContainsRepoCount)

	// CLI project display — pending until the daemon-backed BDD harness is built.
	ctx.Step(`^the project's config\.toml declares:$`, pendingProjectConfigTOMLDeclares)
	ctx.Step(`^the project's config\.toml has no repository entries$`, pendingProjectConfigTOMLNoRepos)
	ctx.Step(`^the project's config\.toml has a repository entry named "([^"]*)" with path "([^"]*)"$`, pendingProjectConfigTOMLHasRepo)
	ctx.Step(`^the user runs "([^"]*)"$`, pendingUserRunsCommand)
	ctx.Step(`^the command succeeds$`, pendingCommandSucceeds)
	ctx.Step(`^the output contains "([^"]*)"$`, pendingOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, pendingOutputNotContains)
	ctx.Step(`^the output contains a deprecation warning about missing repository configuration$`, pendingOutputContainsDeprecationWarning)
	ctx.Step(`^the output contains migration instructions for adding repository configuration$`, pendingOutputContainsMigrationInstructions)

	// Backward compatibility (L2 / persistence layer)
	ctx.Step(`^the project has no stored repositories$`, pendingNoStoredRepos)
	ctx.Step(`^a project database that has been freshly migrated with no repository rows$`, pendingFreshMigration)
	ctx.Step(`^the repositories store is first accessed for that project$`, pendingFirstAccess)
	ctx.Step(`^exactly (\d+) repositor(?:y|ies) (?:is|are) seeded automatically$`, pendingSeededCount)
	ctx.Step(`^the seeded repository is marked as default$`, pendingSeededIsDefault)
	ctx.Step(`^the seeded repository has path equal to the project root directory$`, pendingSeededPath)
}

// ─── DSL parsing steps (L1) ──────────────────────────────────────────────────

func (rc *repositoryContext) clocheFileContaining(content *godog.DocString) error {
	rc.clocheSource = content.Content
	return nil
}

func (rc *repositoryContext) dslParserProcesses() error {
	rc.parsedWorkflows, rc.dslErr = dsl.ParseAll(rc.clocheSource)
	return nil
}

func (rc *repositoryContext) noParseError() error {
	if rc.dslErr != nil {
		return fmt.Errorf("expected no parse error, got: %w", rc.dslErr)
	}
	if rc.parseErr != nil {
		return fmt.Errorf("expected no config parse error, got: %w", rc.parseErr)
	}
	return nil
}

func (rc *repositoryContext) stepHasRepository(stepName, workflowName, repoName string) error {
	wf, ok := rc.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	step, ok := wf.Steps[stepName]
	if !ok {
		return fmt.Errorf("step %q not found in workflow %q", stepName, workflowName)
	}
	got := step.Config["repository"]
	if got != repoName {
		return fmt.Errorf("step %q.repository = %q, want %q", stepName, got, repoName)
	}
	return nil
}

func (rc *repositoryContext) workflowDeclaresRepos(workflowName, reposList string) error {
	wf, ok := rc.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	want := parseQuotedCSV(reposList)
	if !equalStringSlices(wf.Repos, want) {
		return fmt.Errorf("workflow %q repos = %v, want %v", workflowName, wf.Repos, want)
	}
	return nil
}

func (rc *repositoryContext) workflowRepoCount(workflowName string, count int) error {
	wf, ok := rc.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	if len(wf.Repos) != count {
		return fmt.Errorf("workflow %q declares %d repos, want %d", workflowName, len(wf.Repos), count)
	}
	return nil
}

// ─── Config.toml parsing steps (L1) ──────────────────────────────────────────

func (rc *repositoryContext) configTOMLContaining(content *godog.DocString) error {
	rc.configSource = content.Content
	return nil
}

func (rc *repositoryContext) configTOMLContainingNoRepos() error {
	rc.configSource = ""
	return nil
}

func (rc *repositoryContext) configIsParsed() error {
	dir, err := os.MkdirTemp("", "cloche-bdd-config-*")
	if err != nil {
		return err
	}
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(rc.configSource), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	rc.parsedConfig = cfg
	rc.parseErr = err
	return nil
}

func (rc *repositoryContext) configContainsRepoWithPath(name, path string) error {
	if rc.parsedConfig == nil {
		return errors.New("no parsed config")
	}
	for _, r := range rc.parsedConfig.Repositories {
		if r.Name == name {
			if r.Path != path {
				return fmt.Errorf("repository %q has path %q, want %q", name, r.Path, path)
			}
			return nil
		}
	}
	return fmt.Errorf("repository %q not found in config", name)
}

func (rc *repositoryContext) configContainsRepoIsDefault(name string) error {
	if rc.parsedConfig == nil {
		return errors.New("no parsed config")
	}
	for _, r := range rc.parsedConfig.Repositories {
		if r.Name == name {
			if !r.Default {
				return fmt.Errorf("repository %q is not marked as default", name)
			}
			return nil
		}
	}
	return fmt.Errorf("repository %q not found in config", name)
}

func (rc *repositoryContext) configContainsRepoByName(name string) error {
	if rc.parsedConfig == nil {
		return errors.New("no parsed config")
	}
	for _, r := range rc.parsedConfig.Repositories {
		if r.Name == name {
			return nil
		}
	}
	return fmt.Errorf("repository %q not found in config", name)
}

func (rc *repositoryContext) configContainsRepoCount(count int) error {
	if rc.parsedConfig == nil {
		return errors.New("no parsed config")
	}
	got := len(rc.parsedConfig.Repositories)
	if got != count {
		return fmt.Errorf("config contains %d repositories, want %d", got, count)
	}
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func parseQuotedCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── CLI pending stubs (L1 CLI surface — daemon-backed BDD harness TBD) ──────

func pendingProjectConfigTOMLDeclares(config *godog.DocString) error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingProjectConfigTOMLNoRepos() error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingProjectConfigTOMLHasRepo(name, path string) error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingUserRunsCommand(cmd string) error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingCommandSucceeds() error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingOutputContains(text string) error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingOutputNotContains(text string) error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingOutputContainsDeprecationWarning() error {
	return errors.New("pending: L1 CLI surface BDD harness")
}

func pendingOutputContainsMigrationInstructions() error {
	return errors.New("pending: L1 CLI surface BDD harness")
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
