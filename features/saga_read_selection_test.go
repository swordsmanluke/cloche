package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initSagaReadSelectionScenarios) }

func initSagaReadSelectionScenarios(ctx *godog.ScenarioContext) {
	// L1 steps.
	ctx.Step(`^I inspect the stub saga used by the saga module tests$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^it can emit selection-only, selection-plus-text, text-only and multi-select answers$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the new selection read tests run against the unfixed read\.sh$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the selection cases fail and the text-only case passes$`, func() error {
		return godog.ErrPending // pending: L1
	})

	// L2 steps.
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered by selecting position (\d+) with no text$`, func(a, b string, pos int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered by selecting position (\d+) with text "([^"]*)"$`, func(a, b string, pos int, text string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a choice question with options "([^"]*)" and "([^"]*)" answered with text "([^"]*)" and no selection$`, func(a, b, text string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a choice question with options "([^"]*)", "([^"]*)" and "([^"]*)" answered by selecting positions (\d+) and (\d+)$`, func(a, b, c string, p1, p2 int) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a choice question answered with an empty selection and no text$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^read\.sh reads that choice question$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the read output is exactly "([^"]*)"$`, func(want string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the read output is "([^"]*)" followed by the line "([^"]*)"$`, func(first, second string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^I read the read-answer row of the saga module README$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^it says the output has the selected option labels and the text$`, func() error {
		return godog.ErrPending // pending: L2
	})
}
