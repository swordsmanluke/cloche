package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initRecoveryResultNamesScenarios) }

func initRecoveryResultNamesScenarios(ctx *godog.ScenarioContext) {
	// L1 steps: recovery prompt content.
	ctx.Step(`^a step that declares the results (".+")$`, func(results string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt is built for that step$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt names the result "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt does not name the result "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt states that only the declared results are valid$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt asks for a CLOCHE_RESULT marker carrying the run nonce$`, func() error {
		return godog.ErrPending // pending: L1
	})

	// L2 steps: recovery answer handling.
	ctx.Step(`^the agent finishes the step without printing a result marker$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the recovery turn answers with the result "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the step result is "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the run does not abort$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the step's fail wire is taken$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the sub-workflow is not reported as failed with "([^"]*)"$`, func(msg string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a workflow whose implement step declares the results (".+")$`, func(results string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the agent finishes the implement step without printing a result marker$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the implement step's fail wire is taken$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the workflow continues past the implement step$`, func() error {
		return godog.ErrPending // pending: L2
	})
}
