package features_test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cucumber/godog"
)

// tokenLimitCtx holds per-scenario state for token-limit BDD scenarios.
type tokenLimitCtx struct {
	dslContent      string
	parsedWorkflows map[string]*domain.Workflow
	dslParseErr     error

	// Engine simulation state (L2)
	executorCallCount int
	stepResults       map[string]string // step name -> result string
	runFailed         bool
	runAborted        bool
}

func (s *tokenLimitCtx) reset() {
	*s = tokenLimitCtx{}
}

// ─── L1: DSL parsing steps ───────────────────────────────────────────────────

func (s *tokenLimitCtx) aTokenLimitDSLFileContaining(content *godog.DocString) error {
	s.dslContent = content.Content
	return nil
}

func (s *tokenLimitCtx) theTokenLimitDSLFileIsParsed() error {
	workflows, err := dsl.ParseAll(s.dslContent)
	s.parsedWorkflows = workflows
	s.dslParseErr = err
	if err != nil {
		return nil
	}
	for _, wf := range workflows {
		s.dslParseErr = wf.Validate()
		if s.dslParseErr != nil {
			break
		}
	}
	return nil
}

func (s *tokenLimitCtx) noTokenLimitParseErrorIsReturned() error {
	if s.dslParseErr != nil {
		return fmt.Errorf("unexpected parse error: %w", s.dslParseErr)
	}
	return nil
}

func (s *tokenLimitCtx) stepHasConfigValue(stepName, workflowName, key, value string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	step, ok := wf.Steps[stepName]
	if !ok {
		return fmt.Errorf("step %q not found in workflow %q", stepName, workflowName)
	}
	got, ok := step.Config[key]
	if !ok {
		return fmt.Errorf("step %q in workflow %q has no config key %q", stepName, workflowName, key)
	}
	if got != value {
		return fmt.Errorf("step %q config[%q] = %q, want %q", stepName, key, got, value)
	}
	return nil
}

func (s *tokenLimitCtx) workflowHasConfigValue(workflowName, key, value string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	got, ok := wf.Config[key]
	if !ok {
		return fmt.Errorf("workflow %q has no config key %q", workflowName, key)
	}
	if got != value {
		return fmt.Errorf("workflow %q config[%q] = %q, want %q", workflowName, key, got, value)
	}
	return nil
}

func (s *tokenLimitCtx) stepHasImplicitResultWiredTo(stepName, workflowName, result, target string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	for _, w := range wf.Wiring {
		if w.From == stepName && w.Result == result && w.Implicit {
			if w.To != target {
				return fmt.Errorf("step %q has implicit %q wire to %q, want %q", stepName, result, w.To, target)
			}
			return nil
		}
	}
	return fmt.Errorf("step %q in workflow %q has no implicit %q wire", stepName, workflowName, result)
}

func (s *tokenLimitCtx) workflowWireFromAnyStepGoesTo(workflowName, result, target string) error {
	wf, ok := s.parsedWorkflows[workflowName]
	if !ok {
		return fmt.Errorf("workflow %q not found", workflowName)
	}
	for stepName := range wf.Steps {
		found := false
		for _, w := range wf.Wiring {
			if w.From == stepName && w.Result == result {
				if w.To != target {
					return fmt.Errorf("step %q has %q wire to %q, want %q", stepName, result, w.To, target)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("step %q in workflow %q has no %q wire", stepName, workflowName, result)
		}
	}
	return nil
}

func (s *tokenLimitCtx) aTokenLimitParseErrorIsReturned() error {
	if s.dslParseErr == nil {
		return errors.New("expected a parse/validation error but got none")
	}
	return nil
}

func (s *tokenLimitCtx) theTokenLimitErrorMentions(keyword string) error {
	if s.dslParseErr == nil {
		return errors.New("no error to inspect")
	}
	if !strings.Contains(s.dslParseErr.Error(), keyword) {
		return fmt.Errorf("error %q does not mention %q", s.dslParseErr.Error(), keyword)
	}
	return nil
}

// ─── L2: Engine enforcement steps ────────────────────────────────────────────

func (s *tokenLimitCtx) engineWithStepHavingTokenLimit(stepName string, limit int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineWithTwoStepWorkflowTokenLimit(limit int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineWithWorkflowTokenLimit(limit int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) stepCompletesWithOutputTokens(stepName string, outputTokens int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) stepCompletesWithOutputAndInputTokens(stepName string, outputTokens, inputTokens int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) eachStepCompletesWithOutputTokens(outputTokens int) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineExecutesTheWorkflow() error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineStepResultIs(stepName, result string) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineStepResultIsNot(stepName, result string) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineRunIsMarkedFailed() error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineRunIsAbortedAfterFirstStep() error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) engineRunIsNotAborted() error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) executorIsNeverCalledForStep(stepName string) error {
	return godog.ErrPending
}

func (s *tokenLimitCtx) noExecutorIsCalled() error {
	return godog.ErrPending
}

// ─── Step registration ────────────────────────────────────────────────────────

func initTokenLimitScenarios(ctx *godog.ScenarioContext) {
	s := &tokenLimitCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// L1: DSL parsing
	ctx.Step(`^a token-limit DSL file containing:$`, s.aTokenLimitDSLFileContaining)
	ctx.Step(`^the token-limit DSL file is parsed$`, s.theTokenLimitDSLFileIsParsed)
	ctx.Step(`^no token-limit parse error is returned$`, s.noTokenLimitParseErrorIsReturned)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has a "([^"]*)" config value of "([^"]*)"$`, s.stepHasConfigValue)
	ctx.Step(`^workflow "([^"]*)" has a "([^"]*)" config value of "([^"]*)"$`, s.workflowHasConfigValue)
	ctx.Step(`^step "([^"]*)" in workflow "([^"]*)" has an implicit "([^"]*)" result wired to "([^"]*)"$`, s.stepHasImplicitResultWiredTo)
	ctx.Step(`^in workflow "([^"]*)" the wire from any step on "([^"]*)" goes to "([^"]*)"$`, s.workflowWireFromAnyStepGoesTo)
	ctx.Step(`^a token-limit parse error is returned$`, s.aTokenLimitParseErrorIsReturned)
	ctx.Step(`^the token-limit error mentions "([^"]*)"$`, s.theTokenLimitErrorMentions)

	// L2: Engine enforcement
	ctx.Step(`^an engine with a single-step workflow where step "([^"]*)" has token-limit (-?\d+)$`, s.engineWithStepHavingTokenLimit)
	ctx.Step(`^an engine with a two-step workflow where the workflow has token-limit (\d+)$`, s.engineWithTwoStepWorkflowTokenLimit)
	ctx.Step(`^an engine with a single-step workflow where the workflow has token-limit (-?\d+)$`, s.engineWithWorkflowTokenLimit)
	ctx.Step(`^step "([^"]*)" completes reporting (\d+) output tokens$`, s.stepCompletesWithOutputTokens)
	ctx.Step(`^step "([^"]*)" completes reporting (\d+) output tokens and (\d+) input tokens$`, s.stepCompletesWithOutputAndInputTokens)
	ctx.Step(`^each step completes reporting (\d+) output tokens$`, s.eachStepCompletesWithOutputTokens)
	ctx.Step(`^the engine executes the workflow$`, s.engineExecutesTheWorkflow)
	ctx.Step(`^the engine step result for "([^"]*)" is "([^"]*)"$`, s.engineStepResultIs)
	ctx.Step(`^the engine step result for "([^"]*)" is not "([^"]*)"$`, s.engineStepResultIsNot)
	ctx.Step(`^the engine run is marked failed$`, s.engineRunIsMarkedFailed)
	ctx.Step(`^the engine run is aborted after the first step$`, s.engineRunIsAbortedAfterFirstStep)
	ctx.Step(`^the engine run is not aborted$`, s.engineRunIsNotAborted)
	ctx.Step(`^the executor is never called for step "([^"]*)"$`, s.executorIsNeverCalledForStep)
	ctx.Step(`^no executor is called$`, s.noExecutorIsCalled)
}

// suppress unused import warning during the pending phase
var _ = strings.Contains
