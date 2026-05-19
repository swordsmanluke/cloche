package features_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// promptTemplateCtx holds per-scenario state for prompt-templating BDD scenarios.
type promptTemplateCtx struct {
	template      string
	builtins      map[string]string // task_id, run_id, step_name, workdir, prev_output, task_description
	kvStore       map[string]string
	workDir       string // temp directory created per-scenario
	envVars       []envVarRestore // env vars to restore on reset

	resolvedPrompt string
	resolveErr     error
	warnings       []string // captured deprecation warnings (one entry per warning event)
}

type envVarRestore struct {
	key string
	old string // original value; empty means was not set
	was bool   // true if key was set before
}

func (s *promptTemplateCtx) reset() {
	if s.workDir != "" {
		os.RemoveAll(s.workDir)
	}
	for _, e := range s.envVars {
		if e.was {
			os.Setenv(e.key, e.old)
		} else {
			os.Unsetenv(e.key)
		}
	}
	*s = promptTemplateCtx{}
}

func (s *promptTemplateCtx) ensureWorkDir() error {
	if s.workDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "cloche-bdd-template-*")
	if err != nil {
		return fmt.Errorf("creating temp workdir: %w", err)
	}
	s.workDir = dir
	s.builtins["workdir"] = dir
	return nil
}

func initPromptTemplatingScenarios(ctx *godog.ScenarioContext) {
	s := &promptTemplateCtx{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		s.kvStore = make(map[string]string)
		s.builtins = map[string]string{
			"task_id":          "test-task-id",
			"run_id":           "test-run-id",
			"step_name":        "test-step",
			"workdir":          "",
			"prev_output":      "",
			"task_description": "",
		}
		return nil, nil
	})

	// Background
	ctx.Step(`^a clean template workspace$`, s.aCleanTemplateWorkspace)

	// Given — setup (fully implemented; just populate scenario state)
	ctx.Step(`^a prompt template "([^"]*)"$`, s.aPromptTemplate)
	ctx.Step(`^the run has task_id "([^"]*)"$`, s.theRunHasTaskID)
	ctx.Step(`^the run has step_name "([^"]*)"$`, s.theRunHasStepName)
	ctx.Step(`^the run has task_description "([^"]*)"$`, s.theRunHasTaskDescription)
	ctx.Step(`^the run has previous_output "([^"]*)"$`, s.theRunHasPreviousOutput)
	ctx.Step(`^the KV store has "([^"]*)" = "([^"]*)"$`, s.theKVStoreHas)
	ctx.Step(`^the KV store is empty$`, s.theKVStoreIsEmpty)
	ctx.Step(`^a file "([^"]*)" in the workdir containing "([^"]*)"$`, s.aFileInWorkdirContaining)
	ctx.Step(`^"([^"]*)" is an env var with the value "([^"]*)"$`, s.anEnvVarHasValue)

	// Given — L2 daemon (pending until L2 lands)
	ctx.Step(`^the daemon is running with a test KV store$`, s.theDaemonIsRunningWithTestKVStore)

	// When — all pending until the respective layer lands
	ctx.Step(`^the template is resolved$`, s.theTemplateIsResolved)
	ctx.Step(`^the template is resolved with legacy support$`, s.theTemplateIsResolvedWithLegacySupport)
	ctx.Step(`^the template is resolved using the real KV reader$`, s.theTemplateIsResolvedUsingRealKV)

	// Then — fully implemented assertions (execute once When steps pass)
	ctx.Step(`^the resolved prompt contains "([^"]*)"$`, s.theResolvedPromptContains)
	ctx.Step(`^the resolved prompt does not contain "([^"]*)"$`, s.theResolvedPromptNotContains)
	ctx.Step(`^resolution fails with an error mentioning "([^"]*)"$`, s.resolutionFailsMentioning)
	ctx.Step(`^resolution fails with an error mentioning exit status$`, s.resolutionFailsExitStatus)
	ctx.Step(`^a deprecation warning is emitted for "([^"]*)"$`, s.deprecationWarningFor)
	ctx.Step(`^exactly (\d+) deprecation warning(?:s are| is) emitted for "([^"]*)"$`, s.exactlyNDeprecationWarningsFor)
}

// ─── Background ──────────────────────────────────────────────────────────────

func (s *promptTemplateCtx) aCleanTemplateWorkspace() error {
	return s.ensureWorkDir()
}

// ─── Given steps (fully implemented) ─────────────────────────────────────────

func (s *promptTemplateCtx) aPromptTemplate(tmpl string) error {
	s.template = tmpl
	return nil
}

func (s *promptTemplateCtx) theRunHasTaskID(id string) error {
	s.builtins["task_id"] = id
	return nil
}

func (s *promptTemplateCtx) theRunHasStepName(name string) error {
	s.builtins["step_name"] = name
	return nil
}

func (s *promptTemplateCtx) theRunHasTaskDescription(desc string) error {
	s.builtins["task_description"] = desc
	return nil
}

func (s *promptTemplateCtx) theRunHasPreviousOutput(out string) error {
	s.builtins["prev_output"] = out
	return nil
}

func (s *promptTemplateCtx) theKVStoreHas(key, value string) error {
	s.kvStore[key] = value
	return nil
}

func (s *promptTemplateCtx) theKVStoreIsEmpty() error {
	s.kvStore = make(map[string]string)
	return nil
}

func (s *promptTemplateCtx) aFileInWorkdirContaining(name, content string) error {
	if err := s.ensureWorkDir(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.workDir, name), []byte(content), 0644)
}

func (s *promptTemplateCtx) anEnvVarHasValue(key, value string) error {
	old, was := os.LookupEnv(key)
	s.envVars = append(s.envVars, envVarRestore{key: key, old: old, was: was})
	return os.Setenv(key, value)
}

// ─── Given steps — L2 (pending) ──────────────────────────────────────────────

func (s *promptTemplateCtx) theDaemonIsRunningWithTestKVStore() error {
	// The KV store is already populated via theKVStoreHas; the real daemon wiring
	// (gRPC-backed KVReader) is the L2 concern.
	return nil
}

// ─── When steps (pending until layers land) ───────────────────────────────────

func (s *promptTemplateCtx) theTemplateIsResolved() error {
	return errors.New("pending: L1 template resolver implementation")
}

func (s *promptTemplateCtx) theTemplateIsResolvedWithLegacySupport() error {
	return errors.New("pending: L1 template resolver implementation")
}

func (s *promptTemplateCtx) theTemplateIsResolvedUsingRealKV() error {
	return errors.New("pending: L2 KV wiring implementation")
}

// ─── Then steps (fully implemented) ──────────────────────────────────────────

func (s *promptTemplateCtx) theResolvedPromptContains(text string) error {
	if s.resolveErr != nil {
		return fmt.Errorf("resolution failed unexpectedly: %w", s.resolveErr)
	}
	if !strings.Contains(s.resolvedPrompt, text) {
		return fmt.Errorf("resolved prompt does not contain %q\nfull prompt:\n%s", text, s.resolvedPrompt)
	}
	return nil
}

func (s *promptTemplateCtx) theResolvedPromptNotContains(text string) error {
	if s.resolveErr != nil {
		return fmt.Errorf("resolution failed unexpectedly: %w", s.resolveErr)
	}
	if strings.Contains(s.resolvedPrompt, text) {
		return fmt.Errorf("resolved prompt unexpectedly contains %q\nfull prompt:\n%s", text, s.resolvedPrompt)
	}
	return nil
}

func (s *promptTemplateCtx) resolutionFailsMentioning(text string) error {
	if s.resolveErr == nil {
		return fmt.Errorf("expected resolution to fail, but got prompt: %q", s.resolvedPrompt)
	}
	if !strings.Contains(s.resolveErr.Error(), text) {
		return fmt.Errorf("error %q does not mention %q", s.resolveErr.Error(), text)
	}
	return nil
}

func (s *promptTemplateCtx) resolutionFailsExitStatus() error {
	if s.resolveErr == nil {
		return fmt.Errorf("expected resolution to fail with exit-status error, but it succeeded")
	}
	if !strings.Contains(s.resolveErr.Error(), "exit") {
		return fmt.Errorf("error %q does not mention exit status", s.resolveErr.Error())
	}
	return nil
}

func (s *promptTemplateCtx) deprecationWarningFor(pattern string) error {
	for _, w := range s.warnings {
		if strings.Contains(w, pattern) {
			return nil
		}
	}
	return fmt.Errorf("no deprecation warning found for pattern %q; all warnings: %v", pattern, s.warnings)
}

func (s *promptTemplateCtx) exactlyNDeprecationWarningsFor(n int, pattern string) error {
	count := 0
	for _, w := range s.warnings {
		if strings.Contains(w, pattern) {
			count++
		}
	}
	if count != n {
		return fmt.Errorf("expected %d deprecation warning(s) for %q, got %d; all warnings: %v", n, pattern, count, s.warnings)
	}
	return nil
}
