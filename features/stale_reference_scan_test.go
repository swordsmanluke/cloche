package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initStaleReferenceScanScenarios) }

func initStaleReferenceScanScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a git checkout whose tracked files mention no stale saga keys$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^an untracked "([^"]*)" directory that mentions a stale saga key$`, func(dir string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a tracked file "([^"]*)" that mentions a stale saga key$`, func(path string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the stale-reference scan runs$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the scan passes$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the scan fails naming "([^"]*)"$`, func(path string) error {
		return godog.ErrPending // pending: L2
	})
}
