package features_test

import "github.com/cucumber/godog"

// Step definitions for group_binds.feature. All steps are pending until the
// named layer implements them (L1: parse/resolve/validate; L2: run paths+docs).

func init() { registerScenarios(initGroupBindsScenarios) }

func initGroupBindsScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a\ module\ "([^"]*)"\ exporting\ "([^"]*)",\ "([^"]*)"\ and\ "([^"]*)"$`, func(_ string, _ string, _ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ containing\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ workflow\ file\ is\ parsed\ and\ slots\ are\ resolved$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^slots\ "([^"]*)",\ "([^"]*)"\ and\ "([^"]*)"\ resolve\ to\ module\ "([^"]*)"$`, func(_ string, _ string, _ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ module\ "([^"]*)"\ exporting\ all\ six\ tasks\ slots$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ containing\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ six\ tasks\ slots\ resolve\ to\ module\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ resolution\ matches\ the\ six\ per\-slot\ binds\ written\ out\ by\ hand$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ with\ one\ per\-slot\ bind\ for\ each\ of\ the\ six\ tasks\ slots$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^modules\ "([^"]*)"\ and\ "([^"]*)"\ each\ exporting\ the\ qa\ slots$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ containing\ "([^"]*)"\ and\ "([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^slot\ "([^"]*)"\ resolves\ to\ module\ "([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^slots\ "([^"]*)"\ and\ "([^"]*)"\ resolve\ to\ module\ "([^"]*)"$`, func(_ string, _ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ using\ only\ per\-slot\ binds\ and\ no\ settings\ blocks$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ workflow\ file\ is\ parsed$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^parsing\ succeeds\ with\ the\ same\ binds\ as\ before$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ module\ "([^"]*)"\ exporting\ "([^"]*)"\ and\ "([^"]*)"\ but\ not\ "([^"]*)"$`, func(_ string, _ string, _ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ user\ runs\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^validation\ fails$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the\ error\ names\ the\ missing\ export\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ containing\ "([^"]*)"\ where\ the\ target\ is\ not\ a\ module$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^validation\ fails\ with\ an\ error\ that\ the\ group\ bind\ target\ must\ be\ a\ module$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ defining\ a\ workflow\ named\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^validation\ fails\ with\ an\ error\ that\ "([^"]*)"\ is\ a\ reserved\ group\ name$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ module\ "([^"]*)"\ that\ lists\ "([^"]*)"\ as\ a\ credential$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ workflow\ file\ containing\ a\ bind\ of\ "([^"]*)"\ with\ setting\ env\.SAGA_TOKEN\ =\ "([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^validation\ succeeds$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ warning\ names\ "([^"]*)"\ as\ a\ credential\ set\ on\ a\ bind$`, func(_ string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^a\ stub\ module\ "([^"]*)"\ whose\ slots\ echo\ their\ environment$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a\ workflow\ file\ containing\ "([^"]*)"cloche"([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ "([^"]*)"\ slot\ runs\ through\ the\ gRPC\ executor$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ "([^"]*)"\ slot\ runs\ through\ the\ host\ phases$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^both\ runs\ see\ SAGA_PROJECT\ equal\ to\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ daemon\ environment\ sets\ X\ to\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^an\ outer\-scope\ bind\ sets\ X\ to\ "([^"]*)"\ and\ an\ inner\-scope\ bind\ sets\ X\ to\ "([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ slot\ runs$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ run\ sees\ X\ equal\ to\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ step's\ with\ block\ sets\ X\ to\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ run\ sees\ X\ equal\ to\ "([^"]*)"$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a\ stub\ module\ "([^"]*)"\ whose\ contract\ requires\ env\ "([^"]*)"$`, func(_ string, _ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ daemon\ environment\ does\ not\ set\ SAGA_PROJECT$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ "([^"]*)"\ slot\ runs$`, func(_ string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the\ run\ is\ not\ rejected\ for\ a\ missing\ env\ requirement$`, func() error {
		return godog.ErrPending // pending: L2
	})
}
