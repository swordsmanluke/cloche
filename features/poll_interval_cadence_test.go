package features_test

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// pollValidation holds the validator state for the L1 validator scenarios.
var pollValidation struct {
	wf       *domain.Workflow
	warnings []string
}

func init() { registerScenarios(initPollIntervalCadenceScenarios) }

// Every step is pending until the named layer lands:
//
//	L1: validator floor + cadence test (fake clock, stub poll runner)
//	L2: coordinator drives polls from its own timer
func initPollIntervalCadenceScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a workflow whose wait step polls with interval "([^"]*)"$`, func(interval string) error {
		pollValidation.wf = &domain.Workflow{Name: "poll-wf", Steps: map[string]*domain.Step{
			"wait": {Name: "wait", Type: domain.StepTypePoll, Config: map[string]string{"poll": "true", "interval": interval}},
		}}
		pollValidation.warnings = nil
		return nil
	})
	ctx.Step(`^the workflow is validated$`, func() error {
		pollValidation.warnings = pollValidation.wf.ValidateConfig()
		return nil
	})
	ctx.Step(`^the validator reports the interval as below the minimum poll interval$`, func() error {
		for _, w := range pollValidation.warnings {
			if strings.Contains(w, "below the minimum poll interval") {
				return nil
			}
		}
		return fmt.Errorf("no below-minimum warning in %v", pollValidation.warnings)
	})
	ctx.Step(`^the report names the step "([^"]*)" and states the minimum$`, func(step string) error {
		for _, w := range pollValidation.warnings {
			if strings.Contains(w, fmt.Sprintf("step %q", step)) && strings.Contains(w, domain.MinPollInterval.String()) {
				return nil
			}
		}
		return fmt.Errorf("no warning naming step %q and minimum %v in %v", step, domain.MinPollInterval, pollValidation.warnings)
	})
	ctx.Step(`^the validator reports no poll interval problem$`, func() error {
		if len(pollValidation.warnings) != 0 {
			return fmt.Errorf("unexpected warnings: %v", pollValidation.warnings)
		}
		return nil
	})
	ctx.Step(`^a poll step with interval "([^"]*)" and timeout "([^"]*)"$`, func(a0 string, a1 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the orchestration loop ticks every "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^(\d+) minutes of fake time pass$`, func(a0 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the poll script ran about every (\d+) seconds$`, func(a0 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^no gap between consecutive runs exceeds (\d+) seconds plus the tolerance$`, func(a0 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the poll script was not run more often than once per interval$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a poll step "([^"]*)" with interval "([^"]*)" and a poll step "([^"]*)" with interval "([^"]*)"$`, func(a0 string, a1 string, a2 string, a3 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^"([^"]*)" ran about every (\d+) seconds$`, func(a0 string, a1 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the poll script never reports ready$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the step ends as timed out after about (\d+) minutes$`, func(a0 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the poll script is not run again after the timeout$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^each poll script run takes (\d+) seconds$`, func(a0 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^no two runs of the poll script overlap$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a standalone poll step with interval "([^"]*)" and timeout "([^"]*)"$`, func(a0 string, a1 string) error {
		return godog.ErrPending // pending: L2
	})
}
