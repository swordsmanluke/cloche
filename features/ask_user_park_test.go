package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initAskUserParkScenarios) }

func initAskUserParkScenarios(ctx *godog.ScenarioContext) {

	// pending: L1 (reproduction test) / L2 (fix) — see per-step comments in PR.
	ctx.Step(`^an agent step whose agent calls ask_user over /mcp$`, func() error { return godog.ErrPending })      // pending: L1
	ctx.Step(`^a human replies after (\d+) seconds$`, func(int) error { return godog.ErrPending })                  // pending: L1
	ctx.Step(`^the agent receives the reply as the tool result$`, func() error { return godog.ErrPending })         // pending: L1
	ctx.Step(`^the run is not parked$`, func() error { return godog.ErrPending })                                   // pending: L1
	ctx.Step(`^nobody replies for (\d+) seconds$`, func(int) error { return godog.ErrPending })                     // pending: L2
	ctx.Step(`^the agent never receives a timeout result$`, func() error { return godog.ErrPending })               // pending: L2
	ctx.Step(`^the step does not fail$`, func() error { return godog.ErrPending })                                  // pending: L2
	ctx.Step(`^park_after is (\d+) minutes$`, func(int) error { return godog.ErrPending })                          // pending: L2
	ctx.Step(`^nobody replies for (\d+) minutes$`, func(int) error { return godog.ErrPending })                     // pending: L2
	ctx.Step(`^the run is parked$`, func() error { return godog.ErrPending })                                       // pending: L2
	ctx.Step(`^the help thread is in state "([^"]*)"$`, func(string) error { return godog.ErrPending })             // pending: L2
	ctx.Step(`^the ask has been parked$`, func() error { return godog.ErrPending })                                 // pending: L2
	ctx.Step(`^a human runs "cloche threads reply" on the thread$`, func() error { return godog.ErrPending })       // pending: L2
	ctx.Step(`^the run resumes with the reply as the answer to the ask$`, func() error { return godog.ErrPending }) // pending: L2
	ctx.Step(`^an agent step whose agent calls "clo ask" over gRPC$`, func() error { return godog.ErrPending })     // pending: L1
	ctx.Step(`^nobody replies until park_after elapses$`, func() error { return godog.ErrPending })                 // pending: L1
	ctx.Step(`^a reply via "cloche threads reply" resumes the run$`, func() error { return godog.ErrPending })      // pending: L1
}
