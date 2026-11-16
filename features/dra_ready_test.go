package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initDRAReadyScenarios) }

// Step stubs for features/dra_ready.feature. L1 wires the docs steps and the
// shared setup (Given) steps; L2 implements the API behaviour steps.
func initDRAReadyScenarios(ctx *godog.ScenarioContext) {
	// Setup steps: wired in L1.
	ctx.Step(`^a DRA for the domain "([^"]*)"$`, func(domain string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a task labelled "([^"]*)" that is not ready$`, func(label string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a task with no domain label that is not ready$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the daily budget cap is exceeded$`, func() error {
		return godog.ErrPending // pending: L1
	})

	// Actions and outcomes: implemented in L2 (the API change).
	ctx.Step(`^the DRA creates a task labelled "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the DRA creates a task labelled "([^"]*)"$`, func(a string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the DRA creates a task with no domain label and the "([^"]*)" label$`, func(a string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the DRA updates the task to add the "([^"]*)" label$`, func(a string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the DRA updates the task to add the "([^"]*)" and "([^"]*)" labels$`, func(a, b string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the task is created$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the update is accepted$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the request is refused$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the error names the domain "([^"]*)"$`, func(d string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^no task is created$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the task carries the "([^"]*)" label$`, func(l string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the task does not carry the "([^"]*)" label$`, func(l string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the loop does not start a run for the task$`, func() error {
		return godog.ErrPending // pending: L2
	})

	// Docs scenario: L1.
	ctx.Step(`^the DRA documentation is read$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^it states that a DRA may mark any task in its own domain ready on create and update$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^it names ruling "([^"]*)"$`, func(r string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^it no longer says a DRA cannot set ready$`, func() error {
		return godog.ErrPending // pending: L1
	})
}
