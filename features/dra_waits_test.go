package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initDRAWaitsScenarios) }

// All steps are pending until the layer named in each comment implements them.
func initDRAWaitsScenarios(ctx *godog.ScenarioContext) {
	pendingL1 := func(pattern string) { ctx.Step(pattern, func() error { return godog.ErrPending }) } // pending: L1
	pendingL2 := func(pattern string) { ctx.Step(pattern, func() error { return godog.ErrPending }) } // pending: L2

	// Background and L1 steps.
	pendingL1(`^a DRA "([^"]*)" whose project defines the polls "([^"]*)" and "([^"]*)"$`)
	pendingL1(`^the DRA runs "([^"]*)"$`)
	pendingL1(`^the DRA runs "([^"]*)" with that wait's id$`)
	pendingL1(`^the command exits 0$`)
	pendingL1(`^the command exits non-zero$`)
	pendingL1(`^the output contains a new wait id$`)
	pendingL1(`^the output lists that wait with poll "([^"]*)" and note "([^"]*)"$`)
	pendingL1(`^the output says "([^"]*)"$`)
	pendingL1(`^the output lists the polls "([^"]*)"$`)
	pendingL1(`^the output reports an unknown subcommand$`)
	pendingL1(`^no wait is registered$`)
	pendingL1(`^the DRA has a pending wait on "([^"]*)"$`)
	pendingL1(`^"cloche dra waits" no longer lists that wait$`)

	// L2 steps.
	pendingL2(`^the wait is backed by the built-in questions poll for "([^"]*)"$`)
	pendingL2(`^the DRA has a pending wait on "([^"]*)" with note "([^"]*)"$`)
	pendingL2(`^the DRA has another event waiting in its inbox$`)
	pendingL2(`^the poll (prints a CLOCHE_RESULT|exits non-zero|passes its timeout)$`)
	pendingL2(`^the DRA is next woken$`)
	pendingL2(`^the wake-up message contains exactly one wait event$`)
	pendingL2(`^the wait event carries the note "([^"]*)", the result "([^"]*)" and the poll's last output$`)
	pendingL2(`^the wait event is batched in the same message as the other event$`)
	pendingL2(`^the poll exits 0 without a result marker$`)
	pendingL2(`^the DRA receives no wait event$`)
	pendingL2(`^the wait is still pending$`)
	pendingL2(`^the DRA has 20 pending waits$`)
	pendingL2(`^the DRA registers one more wait$`)
	pendingL2(`^the registration is refused as over the 20 pending-wait limit$`)
	pendingL2(`^the registration is refused as under the 1 minute minimum interval$`)
	pendingL2(`^the DRA registers a wait on "([^"]*)" with the argument "([^"]*)"$`)
	pendingL2(`^the poll runs$`)
	pendingL2(`^the poll sees CLOCHE_WAIT_ARG_BRANCH exactly equal to "([^"]*)"$`)
	pendingL2(`^no command from the argument was executed$`)

	// L3 steps.
	ctx.Step(`^the daemon is restarted$`, func() error { return godog.ErrPending })                                                      // pending: L3
	ctx.Step(`^the poll then prints a CLOCHE_RESULT$`, func() error { return godog.ErrPending })                                         // pending: L3
	ctx.Step(`^the DRA's next wake-up contains the wait event for "([^"]*)"$`, func(string) error { return godog.ErrPending })           // pending: L3
	ctx.Step(`^the wait ran as a recorded host run named "([^"]*)" with one poll step$`, func(string) error { return godog.ErrPending }) // pending: L3
}
