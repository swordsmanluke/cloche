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

func init() { registerScenarios(initStaleReferenceScanScenarios) }

// staleScan holds the temp git checkout the givens build and the scan result.
var staleScan struct {
	dir string
	out string
	err error
}

const staleKeyLine = "uses saga_batch_id here\n"

func staleGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = staleScan.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func staleWrite(rel, content string) error {
	p := filepath.Join(staleScan.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func initStaleReferenceScanScenarios(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		staleScan.dir, staleScan.out, staleScan.err = "", "", nil
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if staleScan.dir != "" {
			if err := os.RemoveAll(staleScan.dir); err != nil {
				return c, fmt.Errorf("removing temp checkout: %w", err)
			}
		}
		return c, nil
	})
	ctx.Step(`^a git checkout whose tracked files mention no stale saga keys$`, func() error {
		dir, err := os.MkdirTemp("", "stale-scan-")
		if err != nil {
			return err
		}
		staleScan.dir = dir
		if err := staleGit("init", "-q"); err != nil {
			return err
		}
		if err := staleWrite("README.md", "clean file\n"); err != nil {
			return err
		}
		return staleGit("add", "README.md")
	})
	ctx.Step(`^an untracked "([^"]*)" directory that mentions a stale saga key$`, func(dir string) error {
		return staleWrite(filepath.Join(dir, "main.go"), staleKeyLine)
	})
	ctx.Step(`^a tracked file "([^"]*)" that mentions a stale saga key$`, func(path string) error {
		if err := staleWrite(path, staleKeyLine); err != nil {
			return err
		}
		return staleGit("add", path)
	})
	ctx.Step(`^the stale-reference scan runs$`, func() error {
		staleScan.out, staleScan.err = staleReferenceScan(staleScan.dir)
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
		if staleScan.err != nil {
			return staleScan.err
		}
		if !strings.Contains(staleScan.out, path) {
			return fmt.Errorf("scan output %q does not name %q", staleScan.out, path)
		}
		return nil
	})
}
