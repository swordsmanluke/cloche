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
	"github.com/swordsmanluke/cloche/internal/dsl"
	"github.com/swordsmanluke/cloche/internal/engine"
)

// recoveryWorld holds per-scenario state for the recovery-result-name steps.
type recoveryWorld struct {
	results  []string
	got      domain.StepResult
	err      error
	prompt   string // stdin of the recovery turn, captured by the fake agent
	nonce    string
	executed []string // steps the engine executed (workflow scenarios)
	runErr   error

	wfScenario bool // true when the Given declared a whole workflow
}

func init() { registerScenarios(initRecoveryResultNamesScenarios) }

var recoveryQuoted = regexp.MustCompile(`"([^"]*)"`)

func parseQuotedList(s string) []string {
	var out []string
	for _, m := range recoveryQuoted.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// runAgentStep drives the real prompt adapter with a fake agent script (no
// containers). The first invocation prints no marker; the recovery turn
// (invoked with -c) records its stdin and nonce, then answers with `answer`.
func (w *recoveryWorld) runAgentStep(step *domain.Step, answer string) error {
	dir, err := os.MkdirTemp("", "recovery")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	promptFile := filepath.Join(dir, "recovery-prompt.txt")
	nonceFile := filepath.Join(dir, "recovery-nonce.txt")
	script := filepath.Join(dir, "agent.sh")
	body := "#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then\ncat > '" + promptFile + "'\nprintf %s \"$CLOCHE_RESULT_NONCE\" > '" + nonceFile +
		"'\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:" + answer + "\nexit 0\nfi\ncat > /dev/null\necho 'did work, no marker'\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		return err
	}
	a := &prompt.Adapter{Commands: []string{script}}
	w.got, w.err = a.Execute(context.Background(), step, dir)
	if b, err := os.ReadFile(promptFile); err == nil {
		w.prompt = string(b)
	}
	if b, err := os.ReadFile(nonceFile); err == nil {
		w.nonce = string(b)
	}
	return nil
}

func (w *recoveryWorld) agentStep() *domain.Step {
	return &domain.Step{Name: "implement", Type: domain.StepTypeAgent, Results: w.results, Config: map[string]string{"prompt": "work"}}
}

// validNames extracts the result names the recovery prompt offers as valid.
func (w *recoveryWorld) validNames() ([]string, error) {
	if w.prompt == "" {
		return nil, fmt.Errorf("no recovery prompt was captured")
	}
	const lead = "The only valid result names are: "
	i := strings.Index(w.prompt, lead)
	if i < 0 {
		return nil, fmt.Errorf("recovery prompt lists no valid result names: %q", w.prompt)
	}
	rest := w.prompt[i+len(lead):]
	j := strings.Index(rest, ". ")
	if j < 0 {
		j = len(rest)
	}
	var names []string
	for _, n := range strings.Split(rest[:j], ",") {
		names = append(names, strings.TrimSpace(n))
	}
	return names, nil
}

func initRecoveryResultNamesScenarios(ctx *godog.ScenarioContext) {
	w := &recoveryWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = recoveryWorld{}
		return c, nil
	})

	declare := func(results string) error {
		w.results = parseQuotedList(results)
		return nil
	}
	ctx.Step(`^a step that declares the results (".+")$`, declare)
	ctx.Step(`^a workflow whose implement step declares the results (".+")$`, func(results string) error {
		w.wfScenario = true
		return declare(results)
	})
	ctx.Step(`^the agent finishes the step without printing a result marker$`, func() error { return nil })
	ctx.Step(`^the agent finishes the implement step without printing a result marker$`, func() error { return nil })

	// Recovery prompt content: the prompt is captured from the recovery turn.
	ctx.Step(`^the recovery prompt is built for that step$`, func() error {
		return w.runAgentStep(w.agentStep(), w.results[0])
	})
	ctx.Step(`^the recovery prompt names the result "([^"]*)"$`, func(name string) error {
		names, err := w.validNames()
		if err != nil {
			return err
		}
		for _, n := range names {
			if n == name {
				return nil
			}
		}
		return fmt.Errorf("recovery prompt does not name %q (names: %v)", name, names)
	})
	ctx.Step(`^the recovery prompt does not name the result "([^"]*)"$`, func(name string) error {
		names, err := w.validNames()
		if err != nil {
			return err
		}
		for _, n := range names {
			if n == name {
				return fmt.Errorf("recovery prompt unexpectedly names %q", name)
			}
		}
		return nil
	})
	ctx.Step(`^the recovery prompt states that only the declared results are valid$`, func() error {
		if !strings.Contains(w.prompt, "No other result name is valid") {
			return fmt.Errorf("recovery prompt lacks the only-declared-results statement: %q", w.prompt)
		}
		return nil
	})
	ctx.Step(`^the recovery prompt asks for a CLOCHE_RESULT marker carrying the run nonce$`, func() error {
		if w.nonce == "" {
			return fmt.Errorf("no run nonce captured")
		}
		if !strings.Contains(w.prompt, "CLOCHE_RESULT:"+w.nonce+":") {
			return fmt.Errorf("recovery prompt %q lacks CLOCHE_RESULT:%s:", w.prompt, w.nonce)
		}
		return nil
	})

	// Recovery answer handling.
	ctx.Step(`^the recovery turn answers with the result "([^"]*)"$`, func(name string) error {
		if w.wfScenario {
			return w.runWorkflow(name)
		}
		return w.runAgentStep(w.agentStep(), name)
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

	// Engine-level end to end: real engine + real prompt adapter + fake agent.
	ctx.Step(`^the implement step's fail wire is taken$`, func() error {
		if w.runErr != nil {
			return fmt.Errorf("run aborted: %v", w.runErr)
		}
		if w.got.Result != "fail" {
			return fmt.Errorf("implement result = %q, want fail", w.got.Result)
		}
		return nil
	})
	ctx.Step(`^the workflow continues past the implement step$`, func() error {
		for _, n := range w.executed {
			if n == "after_fail" {
				return nil
			}
		}
		return fmt.Errorf("workflow did not continue past implement; executed %v", w.executed)
	})
}

func (w *recoveryWorld) runWorkflow(answer string) error {
	wf := w.buildWorkflow()
	exec := engine.StepExecutorFunc(func(c context.Context, step *domain.Step) (domain.StepResult, error) {
		w.executed = append(w.executed, step.Name)
		if step.Name != "implement" {
			return domain.StepResult{Result: "success"}, nil
		}
		if err := w.runAgentStep(step, answer); err != nil {
			return domain.StepResult{}, err
		}
		return w.got, w.err
	})
	_, w.runErr = engine.New(exec).Run(context.Background(), wf)
	return nil
}

func (w *recoveryWorld) buildWorkflow() *domain.Workflow {
	wfs, err := dsl.ParseAll(`workflow test {
  step implement {
    prompt = "work"
    results = [` + strings.Join(w.results, ", ") + `]
  }
  step after_fail {
    run = "true"
    results = [success]
  }
  implement:success -> done
  implement:fail -> after_fail
  after_fail:success -> done
}`)
	if err != nil {
		panic(err)
	}
	wf := wfs["test"]
	wf.Steps["implement"].Type = domain.StepTypeAgent
	return wf
}
