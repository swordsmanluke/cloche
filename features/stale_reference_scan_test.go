package features_test

import (
	"fmt"
	"path/filepath"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initStaleReferenceScanScenarios) }

// staleScan holds the checkout the L2 givens will build and the scan result.
// TODO(L2): the givens set dir to a temp git repo; until then it is the real repo.
var staleScan struct {
	dir string
	out string
	err error
}

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
		dir := staleScan.dir
		if dir == "" {
			dir = filepath.Join("..")
		}
		staleScan.out, staleScan.err = staleReferenceScan(dir)
		return nil
	})
	ctx.Step(`^the scan passes$`, func() error {
		if staleScan.err != nil {
			return staleScan.err
		}
		if staleScan.out != "" {
			return fmt.Errorf("stale references in:\n%s", staleScan.out)
		}
		return nil
	})
	ctx.Step(`^the scan fails naming "([^"]*)"$`, func(path string) error {
		return godog.ErrPending // pending: L2
	})
}
