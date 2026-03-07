package domain

import (
	"fmt"
	"strings"
)

const (
	StepDone  = "done"
	StepAbort = "abort"
)

type StepType string

const (
	StepTypeAgent    StepType = "agent"
	StepTypeScript   StepType = "script"
	StepTypeWorkflow StepType = "workflow"
)

type Step struct {
	Name    string
	Type    StepType
	Results []string
	Config  map[string]string
}

type Wire struct {
	From   string
	Result string
	To     string
}

type CollectMode string

const (
	CollectAll CollectMode = "all"
	CollectAny CollectMode = "any"
)

type WireCondition struct {
	Step   string
	Result string
}

type Collect struct {
	Mode       CollectMode
	Conditions []WireCondition
	To         string
}

type Workflow struct {
	Name      string
	Steps     map[string]*Step
	Wiring    []Wire
	Collects  []Collect
	EntryStep string
	Config    map[string]string // workflow-level config (e.g. "container.image")
}

func (w *Workflow) Validate() error {
	if w.EntryStep == "" {
		return fmt.Errorf("workflow %q: no entry step defined", w.Name)
	}
	if _, ok := w.Steps[w.EntryStep]; !ok {
		return fmt.Errorf("workflow %q: entry step %q not found", w.Name, w.EntryStep)
	}

	wired := make(map[string]map[string]bool)
	reachable := map[string]bool{w.EntryStep: true}
	for _, wire := range w.Wiring {
		if wired[wire.From] == nil {
			wired[wire.From] = make(map[string]bool)
		}
		wired[wire.From][wire.Result] = true
		if wire.To != StepDone && wire.To != StepAbort {
			reachable[wire.To] = true
		}
	}

	// Validate collects and count their results as wired
	for _, c := range w.Collects {
		// Validate target step exists
		if c.To != StepDone && c.To != StepAbort {
			if _, ok := w.Steps[c.To]; !ok {
				return fmt.Errorf("workflow %q: collect target step %q not found", w.Name, c.To)
			}
			reachable[c.To] = true
		}

		for _, cond := range c.Conditions {
			// Validate condition step exists
			step, ok := w.Steps[cond.Step]
			if !ok {
				return fmt.Errorf("workflow %q: collect references unknown step %q", w.Name, cond.Step)
			}
			// Validate condition result is declared
			found := false
			for _, r := range step.Results {
				if r == cond.Result {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("workflow %q: collect references undeclared result %q on step %q", w.Name, cond.Result, cond.Step)
			}
			// Mark as wired so the "every result must be wired" check passes
			if wired[cond.Step] == nil {
				wired[cond.Step] = make(map[string]bool)
			}
			wired[cond.Step][cond.Result] = true
		}
	}

	for name, step := range w.Steps {
		for _, result := range step.Results {
			if !wired[name][result] {
				return fmt.Errorf("workflow %q: step %q result %q is not wired", w.Name, name, result)
			}
		}
	}

	for name := range w.Steps {
		if !reachable[name] {
			return fmt.Errorf("workflow %q: step %q is orphaned (unreachable)", w.Name, name)
		}
	}

	return nil
}

// knownStepConfigKeys lists recognized step-level config keys.
// Keys with a "container." prefix are also allowed.
var knownStepConfigKeys = map[string]bool{
	"prompt":        true,
	"run":           true,
	"max_attempts":  true,
	"timeout":       true,
	"agent_command": true,
	"agent_args":    true,
	"results":       true,
	"feedback":      true,
	"workflow_name": true,
	"prompt_step":   true,
}

// ValidateConfig checks step config keys against known keys and returns
// warnings for any unrecognized keys (likely typos).
func (w *Workflow) ValidateConfig() []string {
	var warnings []string
	for name, step := range w.Steps {
		for key := range step.Config {
			if knownStepConfigKeys[key] {
				continue
			}
			if strings.HasPrefix(key, "container.") {
				continue
			}
			if strings.HasPrefix(key, "agent_args.") {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"workflow %q: step %q has unrecognized config key %q", w.Name, name, key))
		}
	}
	return warnings
}

// NextSteps returns all target step names wired from the given (stepName, result) pair.
// Multiple targets indicate fanout — parallel branches launched by the engine.
func (w *Workflow) NextSteps(stepName, result string) ([]string, error) {
	var targets []string
	for _, wire := range w.Wiring {
		if wire.From == stepName && wire.Result == result {
			targets = append(targets, wire.To)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("workflow %q: no wiring for step %q result %q", w.Name, stepName, result)
	}
	return targets, nil
}

// Deprecated: NextStep returns the first target only. Use NextSteps for fanout support.
func (w *Workflow) NextStep(stepName, result string) (string, error) {
	targets, err := w.NextSteps(stepName, result)
	if err != nil {
		return "", err
	}
	return targets[0], nil
}
