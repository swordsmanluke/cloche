package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// pollValidation holds the validator state for the L1 validator scenarios.
var pollValidation struct {
	wf       *domain.Workflow
	warnings []string
}

func init() { registerScenarios(initPollIntervalCadenceScenarios) }

// Validator steps (L1) and cadence steps (L2) run against the real
// PollCoordinator / PollStandalone with a fake clock and a fake poll script.
func initPollIntervalCadenceScenarios(ctx *godog.ScenarioContext) {
	ctx.Step(`^a workflow whose wait step polls with interval "([^"]*)"$`, func(interval string) error {
		pollValidation.wf = &domain.Workflow{Name: "poll-wf", Steps: map[string]*domain.Step{
			"wait": {Name: "wait", Type: domain.StepTypePoll, Config: map[string]string{"poll": "true", "interval": interval}},
		}}
		pollValidation.warnings = nil
		return nil
	})
	ctx.Step(`^the workflow is validated$`, func() error {
		pollValidation.warnings = pollValidation.wf.ValidateConfig()
		return nil
	})
	ctx.Step(`^the validator reports the interval as below the minimum poll interval$`, func() error {
		for _, w := range pollValidation.warnings {
			if strings.Contains(w, "below the minimum poll interval") {
				return nil
			}
		}
		return fmt.Errorf("no below-minimum warning in %v", pollValidation.warnings)
	})
	ctx.Step(`^the report names the step "([^"]*)" and states the minimum$`, func(step string) error {
		for _, w := range pollValidation.warnings {
			if strings.Contains(w, fmt.Sprintf("step %q", step)) && strings.Contains(w, domain.MinPollInterval.String()) {
				return nil
			}
		}
		return fmt.Errorf("no warning naming step %q and minimum %v in %v", step, domain.MinPollInterval, pollValidation.warnings)
	})
	ctx.Step(`^the validator reports no poll interval problem$`, func() error {
		if len(pollValidation.warnings) != 0 {
			return fmt.Errorf("unexpected warnings: %v", pollValidation.warnings)
		}
		return nil
	})
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		cadence = &cadenceWorld{tick: 10 * time.Second, interval: map[string]time.Duration{}}
		return c, nil
	})
	ctx.Step(`^a poll step with interval "([^"]*)" and timeout "([^"]*)"$`, func(interval, timeout string) error {
		return cadence.addPoll("wait", interval, timeout)
	})
	ctx.Step(`^a standalone poll step with interval "([^"]*)" and timeout "([^"]*)"$`, func(interval, timeout string) error {
		cadence.standalone = true
		return cadence.addPoll("wait", interval, timeout)
	})
	ctx.Step(`^a poll step "([^"]*)" with interval "([^"]*)" and a poll step "([^"]*)" with interval "([^"]*)"$`, func(n1, i1, n2, i2 string) error {
		if err := cadence.addPoll(n1, i1, "24h"); err != nil {
			return err
		}
		return cadence.addPoll(n2, i2, "24h")
	})
	ctx.Step(`^the orchestration loop ticks every "([^"]*)"$`, func(d string) (err error) {
		cadence.tick, err = time.ParseDuration(d)
		return err
	})
	ctx.Step(`^the poll script never reports ready$`, func() error { return nil }) // the runner always reports pending
	ctx.Step(`^each poll script run takes (\d+) seconds$`, func(n int) error {
		for _, r := range cadence.runners {
			r.dur = time.Duration(n) * time.Second
		}
		return nil
	})
	ctx.Step(`^(\d+) minutes of fake time pass$`, func(n int) error {
		return cadence.simulate(time.Duration(n) * time.Minute)
	})
	ctx.Step(`^the poll script ran about every (\d+) seconds$`, func(n int) error {
		return cadence.assertCadence("wait", time.Duration(n)*time.Second)
	})
	ctx.Step(`^"([^"]*)" ran about every (\d+) seconds$`, func(name string, n int) error {
		return cadence.assertCadence(name, time.Duration(n)*time.Second)
	})
	ctx.Step(`^no gap between consecutive runs exceeds (\d+) seconds plus the tolerance$`, func(n int) error {
		return cadence.assertCadence("wait", time.Duration(n)*time.Second)
	})
	ctx.Step(`^the poll script was not run more often than once per interval$`, func() error {
		r := cadence.runners["wait"]
		for i := 1; i < len(r.starts); i++ {
			if g := r.starts[i].Sub(r.starts[i-1]); g < cadence.interval["wait"] {
				return fmt.Errorf("runs %d and %d are only %v apart (interval %v)", i-1, i, g, cadence.interval["wait"])
			}
		}
		return nil
	})
	ctx.Step(`^the step ends as timed out after about (\d+) minutes$`, func(n int) error {
		want := time.Duration(n) * time.Minute
		if cadence.endedAfter == 0 {
			return fmt.Errorf("step never timed out")
		}
		if d := cadence.endedAfter - want; d < 0 || d > cadenceTolerance {
			return fmt.Errorf("step ended after %v, want about %v", cadence.endedAfter, want)
		}
		return nil
	})
	ctx.Step(`^the poll script is not run again after the timeout$`, func() error {
		r := cadence.runners["wait"]
		for _, st := range r.starts {
			if st.Sub(cadence.t0) > cadence.endedAfter {
				return fmt.Errorf("poll script ran at +%v, after the timeout at +%v", st.Sub(cadence.t0), cadence.endedAfter)
			}
		}
		return nil
	})
	ctx.Step(`^no two runs of the poll script overlap$`, func() error {
		r := cadence.runners["wait"]
		if len(r.starts) < 2 {
			return fmt.Errorf("only %d runs", len(r.starts))
		}
		for i := 1; i < len(r.starts); i++ {
			if r.starts[i].Before(r.ends[i-1]) {
				return fmt.Errorf("run %d started at %v before run %d ended at %v", i, r.starts[i], i-1, r.ends[i-1])
			}
		}
		return nil
	})
}
