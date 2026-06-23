package features_test

import "github.com/cucumber/godog"

// scenarioInitializers collects each feature's step-definition registrar.
//
// Every feature's _test.go self-registers its initializer from a package-level
// init(), so adding a new feature NEVER edits a shared list. This avoids the
// merge conflicts that previously occurred in TestMain's ScenarioInitializer
// when two features (e.g. two concurrent design-task BDD test plans) both
// appended an init*Scenarios call to the same line.
var scenarioInitializers []func(*godog.ScenarioContext)

// registerScenarios adds a feature's scenario initializer to the suite. Call it
// from a package-level init() in your feature's _test.go file, e.g.:
//
//	func init() { registerScenarios(initMyFeatureScenarios) }
func registerScenarios(fn func(*godog.ScenarioContext)) {
	scenarioInitializers = append(scenarioInitializers, fn)
}

// applyScenarioInitializers runs every registered initializer against the godog
// scenario context. It is wired in as TestMain's ScenarioInitializer.
func applyScenarioInitializers(ctx *godog.ScenarioContext) {
	for _, fn := range scenarioInitializers {
		fn(ctx)
	}
}
