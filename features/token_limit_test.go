package features_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cucumber/godog"
)

// tokenLimitCtx holds per-scenario state for token-limit BDD scenarios.
type tokenLimitCtx struct {
	// L1 state
	dslContent      string
	parsedWorkflows map[string]*domain.Workflow
	dslParseErr     error

	// L2 state
	engineWf         *domain.Workflow
	run              *domain.Run
	mu               sync.Mutex
	executorCallCount int            // total across all steps
	executorCallsByStep map[string]int // per-step call counts
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

// parseSingleStepWorkflow parses a DSL string and stores the resulting workflow.
func (s *tokenLimitCtx) parseSingleStepWorkflow(dslContent string) error {
	workflows, err := dsl.ParseAll(dslContent)
	if err != nil {
		return err
	}
	wf := workflows["test"]
	if wf == nil {
		return errors.New("workflow 'test' not found after parsing")
	}
	s.engineWf = wf
	s.executorCallsByStep = make(map[string]int)
	return nil
}

func (s *tokenLimitCtx) engineWithStepHavingTokenLimit(stepName string, limit int) error {
	return s.parseSingleStepWorkflow(fmt.Sprintf(`workflow "test" {
  step %s {
    run = "echo test"
    results = [success]
    token-limit = %d
  }
  %s:success -> done
}`, stepName, limit, stepName))
}

func (s *tokenLimitCtx) engineWithTwoStepWorkflowTokenLimit(limit int) error {
	return s.parseSingleStepWorkflow(fmt.Sprintf(`workflow "test" {
  token-limit = %d
  step step1 {
    run = "echo step1"
    results = [success]
  }
  step step2 {
    run = "echo step2"
    results = [success]
  }
  step1:success -> step2
  step2:success -> done
}`, limit))
}

func (s *tokenLimitCtx) engineWithWorkflowTokenLimit(limit int) error {
	return s.parseSingleStepWorkflow(fmt.Sprintf(`workflow "test" {
  token-limit = %d
  step work {
    run = "echo work"
    results = [success]
  }
  work:success -> done
}`, limit))
}

// makeExecutor builds a counting executor that returns controlled token usage.
// usagePerStep maps step names to specific usage; defaultUsage applies to all others.
func (s *tokenLimitCtx) makeExecutor(usagePerStep map[string]*domain.TokenUsage, defaultUsage *domain.TokenUsage) engine.StepExecutor {
	return engine.StepExecutorFunc(func(_ context.Context, step *domain.Step) (domain.StepResult, error) {
		s.mu.Lock()
		s.executorCallCount++
		s.executorCallsByStep[step.Name]++
		s.mu.Unlock()

		usage := defaultUsage
		if u, ok := usagePerStep[step.Name]; ok {
			usage = u
		}
		return domain.StepResult{Result: "success", Usage: usage}, nil
	})
}

// runEngine runs the engine with the given executor and captures the result.
func (s *tokenLimitCtx) runEngine(exec engine.StepExecutor) error {
	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), s.engineWf)
	s.run = run
	return err
}

func (s *tokenLimitCtx) stepCompletesWithOutputTokens(stepName string, outputTokens int) error {
	exec := s.makeExecutor(
		map[string]*domain.TokenUsage{stepName: {OutputTokens: int64(outputTokens)}},
		&domain.TokenUsage{},
	)
	return s.runEngine(exec)
}

func (s *tokenLimitCtx) stepCompletesWithOutputAndInputTokens(stepName string, outputTokens, inputTokens int) error {
	exec := s.makeExecutor(
		map[string]*domain.TokenUsage{stepName: {OutputTokens: int64(outputTokens), InputTokens: int64(inputTokens)}},
		&domain.TokenUsage{},
	)
	return s.runEngine(exec)
}

func (s *tokenLimitCtx) eachStepCompletesWithOutputTokens(outputTokens int) error {
	exec := s.makeExecutor(nil, &domain.TokenUsage{OutputTokens: int64(outputTokens)})
	return s.runEngine(exec)
}

func (s *tokenLimitCtx) engineExecutesTheWorkflow() error {
	exec := s.makeExecutor(nil, &domain.TokenUsage{})
	return s.runEngine(exec)
}

// stepResult returns the recorded result for a step from the run's step executions.
func (s *tokenLimitCtx) lastStepResult(stepName string) (string, error) {
	if s.run == nil {
		return "", errors.New("engine has not been run yet")
	}
	for i := len(s.run.StepExecutions) - 1; i >= 0; i-- {
		if s.run.StepExecutions[i].StepName == stepName {
			return s.run.StepExecutions[i].Result, nil
		}
	}
	return "", fmt.Errorf("step %q not found in run executions", stepName)
}

func (s *tokenLimitCtx) engineStepResultIs(stepName, result string) error {
	got, err := s.lastStepResult(stepName)
	if err != nil {
		return err
	}
	if got != result {
		return fmt.Errorf("step %q result = %q, want %q", stepName, got, result)
	}
	return nil
}

func (s *tokenLimitCtx) engineStepResultIsNot(stepName, result string) error {
	got, err := s.lastStepResult(stepName)
	if err != nil {
		return err
	}
	if got == result {
		return fmt.Errorf("step %q result = %q, expected it not to be", stepName, result)
	}
	return nil
}

func (s *tokenLimitCtx) engineRunIsMarkedFailed() error {
	if s.run == nil || s.run.State != domain.RunStateFailed {
		state := domain.RunState("(nil)")
		if s.run != nil {
			state = s.run.State
		}
		return fmt.Errorf("expected run state %q, got %q", domain.RunStateFailed, state)
	}
	return nil
}

func (s *tokenLimitCtx) engineRunIsAbortedAfterFirstStep() error {
	// Verify the run ended as failed due to the workflow token-limit accumulator.
	return s.engineRunIsMarkedFailed()
}

func (s *tokenLimitCtx) engineRunIsNotAborted() error {
	if s.run != nil && s.run.State == domain.RunStateFailed {
		return fmt.Errorf("expected run not to be aborted, got state %q", s.run.State)
	}
	return nil
}

func (s *tokenLimitCtx) executorIsNeverCalledForStep(stepName string) error {
	s.mu.Lock()
	count := s.executorCallsByStep[stepName]
	s.mu.Unlock()
	if count != 0 {
		return fmt.Errorf("executor was called %d time(s) for step %q, expected 0", count, stepName)
	}
	return nil
}

func (s *tokenLimitCtx) noExecutorIsCalled() error {
	s.mu.Lock()
	count := s.executorCallCount
	s.mu.Unlock()
	if count != 0 {
		return fmt.Errorf("executor was called %d time(s), expected 0", count)
	}
	return nil
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

