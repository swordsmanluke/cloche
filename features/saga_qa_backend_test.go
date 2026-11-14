package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initSagaQAScenarios) }

func initSagaQAScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^I\ list\ the\ modules\ under\ \.cloche/modules$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^I\ list\ the\ saga\ module's\ files$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^I\ read\ the\ "([^"]*)"\ section\ of\ docs/modules\.md$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ against\ the\ saga\ backend$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ over\ those\ ids\ against\ the\ saga\ backend$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ for\ that\ id\ against\ the\ saga\ backend$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^I\ run\ "([^"]*)"\ on\ this\ repository$`, func(a0 string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a\ project\ with\ "([^"]*)"\ and\ SAGA_PROJECT\ set\ in\ the\ bind\ settings$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ question\ file\ whose\ backend\.feature\ is\ "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ saga\ question\ with\ a\ judge\ ruling$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ saga\ question\ with\ both\ a\ judge\ ruling\ and\ a\ human\ answer$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ stub\ saga\ binary\ on\ PATH$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^every\ conformance\ check\ passes$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^it\ describes\ binding\ saga\ to\ qa\ with\ SAGA_PROJECT$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^it\ does\ not\ describe\ a\ judge\ workflow\ or\ blocking\ await$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^judge\.md\ and\ asking\.md\ exist$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^judge\.md\ mentions\ "([^"]*)",\ "([^"]*)",\ "([^"]*)"\ and\ "([^"]*)"$`, func(a0 string, a1 string, a2 string, a3 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^no\ file\ in\ the\ repo\ mentions\ saga_batch_id,\ saga_batch_file,\ saga_outcome\ or\ reframe$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^one\ question\ id\ per\ question\ is\ printed$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^questions\ asked\ through\ the\ saga\ backend\ that\ have\ all\ been\ answered$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^questions\ asked\ through\ the\ saga\ backend\ that\ nobody\ has\ answered$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ saga\ scenario\ command\ exits\ 0$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ command\ returns\ promptly$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ printed\ answer\ path\ holds\ the\ human\ answer$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ printed\ answer\ path\ holds\ the\ judge\ ruling$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ saga\ backend\ reports\ no\ open\ questions$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ saga\ backend\ reports\ those\ questions\ as\ open$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ shared\ qa\ conformance\ test\ runs\ the\ saga\ module\ with\ the\ stub\ saga$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ stub\ saga\ recorded\ the\ questions\ under\ the\ bind's\ SAGA_PROJECT\ and\ that\ feature$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^there\ is\ no\ saga\-judge\ module$`, func() error {
		return godog.ErrPending // pending: L2
	})
}
