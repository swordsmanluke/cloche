package features_test

import (
	"github.com/cucumber/godog"
)

func init() { registerScenarios(initSagaRealShapeScenarios) }

func initSagaRealShapeScenarios(ctx *godog.ScenarioContext) {
	pending := func() error { return godog.ErrPending }

	// L1
	ctx.Step(`^a real-shaped saga question with one answer by author "([^"]*)" with select \[1\] and status null$`,
		func(author string) error { return godog.ErrPending }) // pending: L1
	ctx.Step(`^a real-shaped saga question answered by both "([^"]*)" and "([^"]*)"$`,
		func(a, b string) error { return godog.ErrPending }) // pending: L1
	ctx.Step(`^a real-shaped saga question with an agent answer, a later agent answer and a deprecated agent answer after it$`,
		pending) // pending: L1
	ctx.Step(`^a real-shaped saga question with no answers$`, pending)                  // pending: L1
	ctx.Step(`^the saga module reads that question$`, pending)                          // pending: L1
	ctx.Step(`^the read exits 0$`, pending)                                             // pending: L1
	ctx.Step(`^the read exits non-zero$`, pending)                                      // pending: L1
	ctx.Step(`^the read prints the human answer's text$`, pending)                      // pending: L1
	ctx.Step(`^the read does not print the agent answer's text$`, pending)              // pending: L1
	ctx.Step(`^the read prints the later non-deprecated agent answer's text$`, pending) // pending: L1
	ctx.Step(`^the read reports the question has no answer yet$`, pending)              // pending: L1

	// L2
	ctx.Step(`^I inspect the stub saga used by the module and feature tests$`, pending)       // pending: L2
	ctx.Step(`^every stubbed answer has a string author and a null status$`, pending)         // pending: L2
	ctx.Step(`^I search tracked files for the object-shaped author "kind" fixture$`, pending) // pending: L2
	ctx.Step(`^no tracked file contains it$`, pending)                                        // pending: L2
	ctx.Step(`^real-shaped saga questions that have all been answered$`, pending)             // pending: L2
	ctx.Step(`^the saga module awaits those questions$`, pending)                             // pending: L2
	ctx.Step(`^the saga module reports no open questions$`, pending)                          // pending: L2
}
