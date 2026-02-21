package domain

import "fmt"

const (
	StepDone  = "done"
	StepAbort = "abort"
)

type StepType string

const (
	StepTypeAgent  StepType = "agent"
	StepTypeScript StepType = "script"
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

func (w *Workflow) NextStep(stepName, result string) (string, error) {
	for _, wire := range w.Wiring {
		if wire.From == stepName && wire.Result == result {
			return wire.To, nil
		}
	}
	return "", fmt.Errorf("workflow %q: no wiring for step %q result %q", w.Name, stepName, result)
}
