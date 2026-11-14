package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
	"github.com/swordsmanluke/cloche/internal/adapters/agents/prompt"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// recoveryWorld holds per-scenario state for the L2 recovery-answer steps.
type recoveryWorld struct {
	results []string
	answer  string
	got     domain.StepResult
	err     error
}

func init() { registerScenarios(initRecoveryResultNamesScenarios) }

func initRecoveryResultNamesScenarios(ctx *godog.ScenarioContext) {
	w := &recoveryWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = recoveryWorld{}
		return c, nil
	})

	// L1 steps: recovery prompt content.
	ctx.Step(`^the recovery prompt is built for that step$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt names the result "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt does not name the result "([^"]*)"$`, func(name string) error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt states that only the declared results are valid$`, func() error {
		return godog.ErrPending // pending: L1
	})
	ctx.Step(`^the recovery prompt asks for a CLOCHE_RESULT marker carrying the run nonce$`, func() error {
		return godog.ErrPending // pending: L1
	})

	// L2 steps: recovery answer handling.
	quoted := regexp.MustCompile(`"([^"]*)"`)
	ctx.Step(`^a step that declares the results (".+")$`, func(results string) error {
		w.results = nil
		for _, m := range quoted.FindAllStringSubmatch(results, -1) {
			w.results = append(w.results, m[1])
		}
		return nil
	})
	ctx.Step(`^the agent finishes the step without printing a result marker$`, func() error { return nil })
	ctx.Step(`^the recovery turn answers with the result "([^"]*)"$`, func(name string) error {
		w.answer = name
		dir, err := os.MkdirTemp("", "recovery")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		script := filepath.Join(dir, "agent.sh")
		body := "#!/bin/sh\ncat > /dev/null\nif [ \"$1\" = \"-c\" ]; then\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:" + name + "\nexit 0\nfi\necho 'did work, no marker'\n"
		if err := os.WriteFile(script, []byte(body), 0755); err != nil {
			return err
		}
		a := &prompt.Adapter{Commands: []string{script}}
		step := &domain.Step{Name: "implement", Type: domain.StepTypeAgent, Results: w.results, Config: map[string]string{"prompt": "work"}}
		w.got, w.err = a.Execute(context.Background(), step, dir)
		return nil
	})
	ctx.Step(`^the step result is "([^"]*)"$`, func(name string) error {
		if w.got.Result != name {
			return fmt.Errorf("step result = %q, want %q", w.got.Result, name)
		}
		return nil
	})
	ctx.Step(`^the run does not abort$`, func() error { return w.err })
	ctx.Step(`^the step's fail wire is taken$`, func() error {
		if w.err != nil || w.got.Result != "fail" {
			return fmt.Errorf("fail wire not taken: result=%q err=%v", w.got.Result, w.err)
		}
		return nil
	})
	ctx.Step(`^the sub-workflow is not reported as failed with "([^"]*)"$`, func(msg string) error {
		if w.err != nil && strings.Contains(w.err.Error(), msg) {
			return fmt.Errorf("unexpected error: %v", w.err)
		}
		return nil
	})
	ctx.Step(`^a workflow whose implement step declares the results (".+")$`, func(results string) error {
		return godog.ErrPending // pending: engine-level end-to-end wiring not covered by this layer's adapter test
	})
	ctx.Step(`^the agent finishes the implement step without printing a result marker$`, func() error {
		return godog.ErrPending // pending
	})
	ctx.Step(`^the implement step's fail wire is taken$`, func() error {
		return godog.ErrPending // pending
	})
	ctx.Step(`^the workflow continues past the implement step$`, func() error {
		return godog.ErrPending // pending
	})
}
