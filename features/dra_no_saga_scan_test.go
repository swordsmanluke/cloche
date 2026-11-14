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

// gitInitAndStage initialises a git repository in dir and stages everything in it.
func gitInitAndStage(dir string) error {
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v\n%s", args, err, out)
		}
	}
	return nil
}

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

	ctx.Step(`^a checkout with an untracked \.gitworktrees/ directory whose files mention "([^"]*)"$`, func(term string) error {
		dir, err := os.MkdirTemp("", "scan-checkout-")
		if err != nil {
			return err
		}
		repo = dir
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("clean\n"), 0o644); err != nil {
			return err
		}
		if err := gitInitAndStage(dir); err != nil {
			return err
		}
		// Created after staging so it stays untracked.
		wt := filepath.Join(dir, ".gitworktrees", "x")
		if err := os.MkdirAll(wt, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(wt, "f.txt"), []byte("uses "+term+"\n"), 0o644)
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
		if err := gitInitAndStage(dir); err != nil {
			return err
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
