package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initGenericJudgeScenarios) }

// Every step is pending until the layer named in its comment implements it.
func initGenericJudgeScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a judge project with a module bound to qa that ships a judge\.md$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge user asks two questions$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the ask exits 0 and prints two question ids$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^exactly one judge run was started carrying those two ids$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge user asks two questions with \-\-no\-judge$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^no judge run was started$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge config sets "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the bound module has no judge\.md$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge user runs "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the validate output warns that the qa module has no judge\.md$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the validate output has no judge\.md warning$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge prompt states the rules to cite sources, never replace a human answer, leave unsure questions unanswered, state a false premise as the answer, and leave permission requests unanswered$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the judge prompt ends with the contents of the module's judge\.md$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the judge run appears in run history with token usage$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the judge budget launch gate is closed$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the project defines its own workflow named "([^"]*)"$`, func(a0 string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the judge run uses the project workflow, not the built\-in one$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^the judge run will fail$`, func() error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^both questions remain unanswered$`, func() error {
		return godog.ErrPending // pending: L2
	})
}
