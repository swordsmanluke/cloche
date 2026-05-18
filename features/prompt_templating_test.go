package features_test

import (
	"context"
	"errors"
	"time"

	"github.com/cucumber/godog"
)

// promptTemplatingCtx holds state for prompt-templating scenarios.
type promptTemplatingCtx struct {
	builtins      map[string]string
	kv            map[string]string
	files         map[string]string
	shellTimeout  time.Duration
	resolved      string
	resolveErr    error
	deprecationWarns map[string]int
}

func (s *promptTemplatingCtx) reset() {
	s.builtins = map[string]string{
		"task_id":         "builtin-42",
		"run_id":          "builtin-99",
		"step_name":       "test-step",
		"workdir":         "/tmp/work",
		"prev_output":     "previous stdout",
		"task_description": "Fix bug",
	}
	s.kv = map[string]string{}
	s.files = map[string]string{}
	s.shellTimeout = 30 * time.Second
	s.resolved = ""
	s.resolveErr = nil
	s.deprecationWarns = map[string]int{}
}

func initPromptTemplatingScenarios(ctx *godog.ScenarioContext) {
	s := &promptTemplatingCtx{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^a test resolver with built-ins and an empty KV store$`, s.aTestResolver)

	// Given
	ctx.Step(`^the KV store contains "([^"]*)" = "([^"]*)"$`, s.theKVStoreContains)
	ctx.Step(`^the built-ins contain "([^"]*)" = "([^"]*)"$`, s.theBuiltInsContain)
	ctx.Step(`^a file "([^"]*)" containing "([^"]*)"$`, s.aFileContaining)
	ctx.Step(`^the shell timeout is (\d+)s$`, s.theShellTimeoutIs)
	ctx.Step(`^a KV key "([^"]*)" has been set to "([^"]*)"$`, s.theKVStoreContains)
	ctx.Step(`^a container step sets KV key "([^"]*)" to "([^"]*)"$`, s.theKVStoreContains)

	// When
	ctx.Step(`^a prompt template "([^"]*)" is resolved$`, s.aPromptTemplateIsResolved)
	ctx.Step(`^a prompt template "([^"]*)" is resolved with user prompt "([^"]*)"$`, s.aPromptTemplateIsResolvedWithUserPrompt)
	ctx.Step(`^a prompt template "([^"]*)" is resolved with previous output "([^"]*)"$`, s.aPromptTemplateIsResolvedWithPreviousOutput)
	ctx.Step(`^a host step prompt "([^"]*)" is resolved$`, s.aPromptTemplateIsResolved)
	ctx.Step(`^a container step prompt "([^"]*)" is resolved$`, s.aPromptTemplateIsResolved)

	// Then
	ctx.Step(`^the resolved prompt is "([^"]*)"$`, s.theResolvedPromptIs)
	ctx.Step(`^the resolved prompt contains "([^"]*)"$`, s.theResolvedPromptContains)
	ctx.Step(`^resolution fails with error containing "([^"]*)"$`, s.resolutionFailsWithErrorContaining)
	ctx.Step(`^exactly one deprecation warning is emitted for "([^"]*)"$`, s.exactlyOneDeprecationWarningIsEmittedFor)
}

// ─── step implementations ────────────────────────────────────────────────────

func (s *promptTemplatingCtx) aTestResolver() error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) theKVStoreContains(key, value string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) theBuiltInsContain(name, value string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) aFileContaining(path, content string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) theShellTimeoutIs(seconds int) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) aPromptTemplateIsResolved(tmpl string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) aPromptTemplateIsResolvedWithUserPrompt(tmpl, userPrompt string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) aPromptTemplateIsResolvedWithPreviousOutput(tmpl, prev string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) theResolvedPromptIs(expected string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) theResolvedPromptContains(expected string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) resolutionFailsWithErrorContaining(expected string) error {
	return errors.New("pending: L1 template resolver engine")
}

func (s *promptTemplatingCtx) exactlyOneDeprecationWarningIsEmittedFor(pattern string) error {
	return errors.New("pending: L1 template resolver engine")
}
