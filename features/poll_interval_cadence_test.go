package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initPollIntervalCadenceScenarios) }

// Every step is pending until the named layer lands:
//
//	L1: validator floor + cadence test (fake clock, stub poll runner)
//	L2: coordinator drives polls from its own timer
func initPollIntervalCadenceScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a workflow whose wait step polls with interval "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the workflow is validated$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the validator reports the interval as below the minimum poll interval$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the report names the step "([^"]*)" and states the minimum$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the validator reports no poll interval problem$`, func() error {
		return godog.ErrPending // pending: L1
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
