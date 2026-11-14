package features_test

import (
	"github.com/cucumber/godog"
)

func init() { registerScenarios(initQAPhaseFailureOutputScenarios) }

// Steps are pending until the named layer implements them. Do not return a
// plain error here: that would fail go test for every layer above.
func initQAPhaseFailureOutputScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a project whose bound qa module has phase scripts that fail with "([^"]*)"$`, func(msg string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the failing script prints (\d+) lines before "([^"]*)"$`, func(n int, msg string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the qa module's phase scripts succeed$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)"$`, func(cmd string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" against the real daemon$`, func(cmd string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output user runs "([^"]*)" from a workflow run$`, func(cmd string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output command exits non-zero$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the qa phase failure output command exits 0$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output error contains "([^"]*)"$`, func(s string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the qa phase failure output error does not contain "([^"]*)"$`, func(s string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the qa phase failure output error names the failing step "([^"]*)"$`, func(step string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the qa phase failure output error is shorter than the full script output$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the calling run's step log file for the failing step contains "([^"]*)"$`, func(s string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^"cloche (logs|poll)" for the calling run contains "([^"]*)"$`, func(cmd, s string) error {
		return godog.ErrPending // pending: L2
	})
}
