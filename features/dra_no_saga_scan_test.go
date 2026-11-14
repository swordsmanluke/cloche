package features_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initDRANoSagaScanScenarios) }

func initDRANoSagaScanScenarios(ctx *godog.ScenarioContext) {
	var repo string
	var found []string

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if repo != "" {
			err := os.RemoveAll(repo)
			repo = ""
			return c, err
		}
		return c, nil
	})

	ctx.Step(`^a checkout with an untracked \.gitworktrees/ directory whose files mention "([^"]*)"$`, func(string) error {
		return godog.ErrPending // pending: L2
	})
	ctx.Step(`^a checkout where the tracked file "([^"]*)" mentions "([^"]*)"$`, func(path, term string) error {
		dir, err := os.MkdirTemp("", "scan-checkout-")
		if err != nil {
			return err
		}
		repo = dir
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte("uses "+term+"\n"), 0o644); err != nil {
			return err
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
			c := exec.Command("git", args...)
			c.Dir = dir
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("git %v: %v\n%s", args, err, out)
			}
		}
		return nil
	})
	ctx.Step(`^the repository is scanned for "([^"]*)"$`, func(term string) error {
		var err error
		found, err = trackedMentions(repo, term)
		return err
	})
	ctx.Step(`^the scan reports no matches$`, func() error {
		if len(found) > 0 {
			return fmt.Errorf("unexpected matches: %v", found)
		}
		return nil
	})
	ctx.Step(`^the scan reports "([^"]*)"$`, func(path string) error {
		if strings.Join(found, ",") != path {
			return fmt.Errorf("scan reported %v, want [%s]", found, path)
		}
		return nil
	})
}
