package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initDRANoSagaScanScenarios) }

func initDRANoSagaScanScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a checkout with an untracked \.gitworktrees/ directory whose files mention "([^"]*)"$`, func(string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a checkout where the tracked file "([^"]*)" mentions "([^"]*)"$`, func(string, string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the repository is scanned for "([^"]*)"$`, func(string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the scan reports no matches$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the scan reports "([^"]*)"$`, func(string) error {
		return godog.ErrPending // pending: L1
	})
}
