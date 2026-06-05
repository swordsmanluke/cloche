package features_test

import (
	"context"

	"github.com/cucumber/godog"
)

// stepTokenMetricsCtx holds per-scenario state for step-token-metrics BDD scenarios.
type stepTokenMetricsCtx struct{}

func (s *stepTokenMetricsCtx) reset() {
	*s = stepTokenMetricsCtx{}
}

func initStepTokenMetricsScenarios(ctx *godog.ScenarioContext) {
	s := &stepTokenMetricsCtx{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// All step definitions below are pending until the metrics query layer
	// (cloche metrics command, QueryStepTokens method, GetStepMetrics RPC) lands.
	//
	// Steps that already exist in repository_test.go (daemon background, user runs,
	// command succeeds, output contains/not-contains) are intentionally omitted here;
	// they resolve to those implementations and are skipped once a preceding Given
	// step returns ErrPending.

	pending1s := func(_ context.Context, _ string) error { return godog.ErrPending }
	pending2ss := func(_ context.Context, _, _ string) error { return godog.ErrPending }
	pending1i := func(_ context.Context, _ int) error { return godog.ErrPending }
	pending3sii := func(_ context.Context, _ string, _, _ int) error { return godog.ErrPending }
	pending3sis := func(_ context.Context, _ string, _ int, _ string) error { return godog.ErrPending }
	pending0 := func(_ context.Context) error { return godog.ErrPending }

	// Given — step-token-specific setup (all pending)
	ctx.Step(`^the project has completed runs of the "([^"]*)" workflow$`, pending1s)
	ctx.Step(`^the project has completed runs of the "([^"]*)" workflow on multiple days$`, pending1s)
	ctx.Step(`^the "([^"]*)" step used (\d+) input tokens and (\d+) output tokens(?: in one run| in another run)?$`, pending3sii)
	ctx.Step(`^the "([^"]*)" step used (\d+) tokens on "([^"]*)"$`, pending3sis)

	// Then — step-token-specific assertions (all pending)
	ctx.Step(`^"([^"]*)" appears before "([^"]*)" in the output$`, pending2ss)
	ctx.Step(`^the output is valid JSON$`, pending0)
	ctx.Step(`^the JSON output contains a "([^"]*)" field$`, pending1s)
	ctx.Step(`^the output shows a total of (\d+) tokens$`, pending1i)
	ctx.Step(`^the output does not show a total of (\d+) tokens$`, pending1i)
}
