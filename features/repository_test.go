package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cucumber/godog"
)

// ─── shared scenario state ────────────────────────────────────────────────────

type repositoryCtx struct {
	// config.toml scenarios
	configContent  string
	parsedConfig   *config.Config
	configParseErr error

	// DSL scenarios
	dslContent      string
	parsedWorkflows map[string]*domain.Workflow
	dslParseErr     error
}

func (s *repositoryCtx) reset() {
	*s = repositoryCtx{}
}

// ─── step registrations ──────────────────────────────────────────────────────

func initRepositoryScenarios(ctx *godog.ScenarioContext) {
	s := &repositoryCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background (CLI feature — L2 pending)
	ctx.Step(`^the daemon is running against a test project directory$`, pendingDaemonRunning)

	// DSL parsing
	ctx.Step(`^a \.cloche file containing:$`, s.aClocheFileContaining)
	ctx.Step(`^the DSL parser processes the file$`, s.theDSLParserProcessesTheFile)
	ctx.Step(`^no parse error is returned$`, s.noParsedError)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has repository "([^"]*)"$`, s.stepHasRepository)
	ctx.Step(`^workflow "([^"]*)" declares repos \[([^\]]*)\]$`, s.workflowDeclaredRepos)
	ctx.Step(`^workflow "([^"]*)" declares (\d+) repos$`, s.workflowRepoCount)

	// Config.toml parsing
	ctx.Step(`^a config\.toml containing:$`, s.aConfigTOMLContaining)
	ctx.Step(`^a config\.toml containing no repository entries$`, s.aConfigTOMLContainingNoRepos)
	ctx.Step(`^the config is parsed$`, s.theConfigIsParsed)
	ctx.Step(`^the config contains a repository named "([^"]*)" with path "([^"]*)"$`, s.configContainsRepoWithPath)
	ctx.Step(`^the config contains a repository named "([^"]*)" marked as default$`, s.configContainsRepoIsDefault)
	ctx.Step(`^the config contains a repository named "([^"]*)"$`, s.configContainsRepoByName)
	ctx.Step(`^the config contains (\d+) repositor(?:y|ies)$`, s.configContainsRepoCount)

	// CLI project display — L1 steps that integrate with daemon are L2 pending
	ctx.Step(`^the project's config\.toml declares:$`, pendingProjectConfigTOMLDeclares)
	ctx.Step(`^the project's config\.toml has no repository entries$`, pendingProjectConfigTOMLNoRepos)
	ctx.Step(`^the project's config\.toml has a repository entry named "([^"]*)" with path "([^"]*)"$`, pendingProjectConfigTOMLHasRepo)
	ctx.Step(`^the user runs "([^"]*)"$`, pendingUserRunsCommand)
	ctx.Step(`^the command succeeds$`, pendingCommandSucceeds)
	ctx.Step(`^the output contains "([^"]*)"$`, pendingOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, pendingOutputNotContains)
	ctx.Step(`^the output contains a deprecation warning about missing repository configuration$`, pendingOutputContainsDeprecationWarning)
	ctx.Step(`^the output contains migration instructions for adding repository configuration$`, pendingOutputContainsMigrationInstructions)

	// Backward compatibility — L2 pending
	ctx.Step(`^the project has no stored repositories$`, pendingNoStoredRepos)
	ctx.Step(`^a project database that has been freshly migrated with no repository rows$`, pendingFreshMigration)
	ctx.Step(`^the repositories store is first accessed for that project$`, pendingFirstAccess)
	ctx.Step(`^exactly (\d+) repositor(?:y|ies) (?:is|are) seeded automatically$`, pendingSeededCount)
	ctx.Step(`^the seeded repository is marked as default$`, pendingSeededIsDefault)
	ctx.Step(`^the seeded repository has path equal to the project root directory$`, pendingSeededPath)
}

// ─── DSL implementations (L1) ────────────────────────────────────────────────

func (s *repositoryCtx) aClocheFileContaining(content *godog.DocString) error {
	s.dslContent = content.Content
	return nil
}

func (s *repositoryCtx) theDSLParserProcessesTheFile() error {
	workflows, err := dsl.ParseAll(s.dslContent)
	s.parsedWorkflows = workflows
	s.dslParseErr = err
	return nil
}

func (s *repositoryCtx) noParsedError() error {
	if s.configParseErr != nil {
		return fmt.Errorf("unexpected config parse error: %w", s.configParseErr)
	}
	if s.dslParseErr != nil {
		return fmt.Errorf("unexpected DSL parse error: %w", s.dslParseErr)
	}
	return nil
}

func (s *repositoryCtx) stepHasRepository(stepName, workflowName, repoName string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	step, ok := wf.Steps[stepName]
	if !ok {
		return fmt.Errorf("step %q not found in workflow %q", stepName, workflowName)
	}
	got, ok := step.Config["repository"]
	if !ok {
		return fmt.Errorf("step %q has no repository field", stepName)
	}
	if got != repoName {
		return fmt.Errorf("step %q: expected repository %q, got %q", stepName, repoName, got)
	}
	return nil
}

func (s *repositoryCtx) workflowDeclaredRepos(workflowName, reposList string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	expected := parseQuotedStringList(reposList)
	if len(wf.Repos) != len(expected) {
		return fmt.Errorf("workflow %q: expected repos %v, got %v", workflowName, expected, wf.Repos)
	}
	for i, e := range expected {
		if wf.Repos[i] != e {
			return fmt.Errorf("workflow %q repos[%d]: expected %q, got %q", workflowName, i, e, wf.Repos[i])
		}
	}
	return nil
}

func (s *repositoryCtx) workflowRepoCount(workflowName string, count int) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	if len(wf.Repos) != count {
		return fmt.Errorf("workflow %q: expected %d repos, got %d", workflowName, count, len(wf.Repos))
	}
	return nil
}

// parseQuotedStringList parses a comma-separated list of quoted strings like:
// `"backend"` or `"candy", "cloche"` into []string{"backend"} / []string{"candy","cloche"}.
func parseQuotedStringList(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"`)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// ─── Config.toml implementations (L1) ────────────────────────────────────────

func (s *repositoryCtx) aConfigTOMLContaining(content *godog.DocString) error {
	s.configContent = content.Content
	return nil
}

func (s *repositoryCtx) aConfigTOMLContainingNoRepos() error {
	s.configContent = ""
	return nil
}

func (s *repositoryCtx) theConfigIsParsed() error {
	tmpDir, err := os.MkdirTemp("", "cloche-config-test-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	clocheDir := filepath.Join(tmpDir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0755); err != nil {
		return fmt.Errorf("creating .cloche dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(s.configContent), 0644); err != nil {
		return fmt.Errorf("writing config.toml: %w", err)
	}

	cfg, err := config.Load(tmpDir)
	s.parsedConfig = cfg
	s.configParseErr = err
	return nil
}

func (s *repositoryCtx) configContainsRepoByName(name string) error {
	for _, r := range s.parsedConfig.Repositories {
		if r.Name == name {
			return nil
		}
	}
	return fmt.Errorf("no repository named %q in config", name)
}

func (s *repositoryCtx) configContainsRepoWithPath(name, path string) error {
	for _, r := range s.parsedConfig.Repositories {
		if r.Name == name && r.Path == path {
			return nil
		}
	}
	return fmt.Errorf("no repository named %q with path %q in config", name, path)
}

func (s *repositoryCtx) configContainsRepoIsDefault(name string) error {
	for _, r := range s.parsedConfig.Repositories {
		if r.Name == name && r.Default {
			return nil
		}
	}
	return fmt.Errorf("no repository named %q marked as default in config", name)
}

func (s *repositoryCtx) configContainsRepoCount(count int) error {
	if len(s.parsedConfig.Repositories) != count {
		return fmt.Errorf("expected %d repositories, got %d", count, len(s.parsedConfig.Repositories))
	}
	return nil
}

// ─── CLI pending stubs (L1 — require daemon integration, implemented in L2) ──

func pendingProjectConfigTOMLDeclares(config *godog.DocString) error {
	return godog.ErrPending
}

func pendingProjectConfigTOMLNoRepos() error {
	return godog.ErrPending
}

func pendingProjectConfigTOMLHasRepo(name, path string) error {
	return godog.ErrPending
}

func pendingUserRunsCommand(cmd string) error {
	return godog.ErrPending
}

func pendingCommandSucceeds() error {
	return godog.ErrPending
}

func pendingOutputContains(text string) error {
	return godog.ErrPending
}

func pendingOutputNotContains(text string) error {
	return godog.ErrPending
}

func pendingOutputContainsDeprecationWarning() error {
	return godog.ErrPending
}

func pendingOutputContainsMigrationInstructions() error {
	return godog.ErrPending
}

// ─── L2 pending stubs ────────────────────────────────────────────────────────

func pendingDaemonRunning() error {
	return godog.ErrPending
}

func pendingNoStoredRepos() error {
	return godog.ErrPending
}

func pendingFreshMigration() error {
	return godog.ErrPending
}

func pendingFirstAccess() error {
	return godog.ErrPending
}

func pendingSeededCount(count int) error {
	return godog.ErrPending
}

func pendingSeededIsDefault() error {
	return godog.ErrPending
}

func pendingSeededPath() error {
	return godog.ErrPending
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
