package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

// promptMigrationCtx holds per-scenario state for the prompt-migration BDD scenarios.
type promptMigrationCtx struct {
	promptName  string
	runCtx      map[string]string // builtins and KV values supplied by Given steps
	scratchDir  string
	fileContent string // raw content of the loaded prompt file
	assembled   string
	assembleErr error
	kvNamespace map[string]string
}

func (s *promptMigrationCtx) reset() {
	*s = promptMigrationCtx{}
}

func initPromptMigrationScenarios(ctx *godog.ScenarioContext) {
	s := &promptMigrationCtx{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		s.runCtx = make(map[string]string)
		s.kvNamespace = make(map[string]string)
		return nil, nil
	})

	// Given
	ctx.Step(`^the wrapper prompt "([^"]*)" is loaded$`, s.wrapperPromptIsLoaded)
	ctx.Step(`^the run context has ([a-z_]+) "([^"]*)"$`, s.runContextHas)
	ctx.Step(`^a scratch file "([^"]*)" contains "([^"]*)"$`, s.scratchFileContains)
	ctx.Step(`^a fresh KV namespace without "([^"]*)"$`, s.freshKVNamespaceWithout)
	ctx.Step(`^a fresh KV namespace with "([^"]*)" = "([^"]*)"$`, s.freshKVNamespaceWith)

	// When
	ctx.Step(`^the wrapper prompt is assembled$`, s.wrapperPromptIsAssembled)
	ctx.Step(`^the prompt file content is scanned for legacy patterns$`, s.promptFileContentIsScanned)
	ctx.Step(`^claim-task\.sh runs against that namespace$`, s.claimTaskSHRuns)

	// Then
	ctx.Step(`^the assembled prompt contains "([^"]*)"$`, s.assembledPromptContains)
	ctx.Step(`^the assembled prompt does not contain "([^"]*)"$`, s.assembledPromptNotContains)
	ctx.Step(`^the prompt file does not contain the pattern "([^"]*)"$`, s.promptFileNotContainsPattern)
	ctx.Step(`^the namespace has "([^"]*)" = "([^"]*)"$`, s.namespaceHas)
}

// ─── Given ───────────────────────────────────────────────────────────────────

func (s *promptMigrationCtx) wrapperPromptIsLoaded(name string) error {
	s.promptName = name
	return errors.New("pending: L1/L2 implementation — load wrapper prompt from disk and store content")
}

func (s *promptMigrationCtx) runContextHas(key, value string) error {
	s.runCtx[key] = value
	return errors.New("pending: L1/L2 implementation — populate resolver builtins/KV from run context")
}

func (s *promptMigrationCtx) scratchFileContains(name, content string) error {
	return errors.New("pending: L2 implementation — write scratch file to temp dir for resolver")
}

func (s *promptMigrationCtx) freshKVNamespaceWithout(key string) error {
	delete(s.kvNamespace, key)
	return errors.New("pending: L2 implementation — set up in-process KV namespace without the named key")
}

func (s *promptMigrationCtx) freshKVNamespaceWith(key, value string) error {
	s.kvNamespace[key] = value
	return errors.New("pending: L2 implementation — set up in-process KV namespace with the named key pre-set")
}

// ─── When ────────────────────────────────────────────────────────────────────

func (s *promptMigrationCtx) wrapperPromptIsAssembled() error {
	return errors.New("pending: L1/L2 implementation — resolve loaded prompt through the template resolver")
}

func (s *promptMigrationCtx) promptFileContentIsScanned() error {
	return errors.New("pending: L1 implementation — read loaded prompt file content for pattern inspection")
}

func (s *promptMigrationCtx) claimTaskSHRuns() error {
	return errors.New("pending: L2 implementation — execute claim-task.sh against the in-process KV namespace")
}

// ─── Then ────────────────────────────────────────────────────────────────────

func (s *promptMigrationCtx) assembledPromptContains(text string) error {
	return errors.New("pending: L1/L2 implementation — assert resolved prompt contains expected text")
}

func (s *promptMigrationCtx) assembledPromptNotContains(text string) error {
	return errors.New("pending: L1/L2 implementation — assert resolved prompt does not contain legacy pattern")
}

func (s *promptMigrationCtx) promptFileNotContainsPattern(pattern string) error {
	return errors.New("pending: L1 implementation — assert raw prompt file does not contain legacy pattern")
}

func (s *promptMigrationCtx) namespaceHas(key, value string) error {
	return errors.New("pending: L2 implementation — assert KV namespace has expected key/value after claim script")
}
