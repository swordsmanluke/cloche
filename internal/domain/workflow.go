package domain

import (
	"fmt"
	"strings"
	"time"
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
	StepTypeHuman    StepType = "human"
)

// DefaultHumanStepTimeout is the default timeout for human steps when no
// explicit timeout is configured. Human steps wait for external input (e.g.
// code review, approval) so they use a much longer default than other step types.
const DefaultHumanStepTimeout = 72 * time.Hour

// WorkflowLocation indicates where a workflow is intended to run.
type WorkflowLocation string

const (
	// LocationContainer is for workflows that run inside a Docker container.
	// This is the default for workflows without a "host { }" block.
	LocationContainer WorkflowLocation = "container"

	// LocationHost is for workflows that run on the host machine.
	// A workflow declares itself as host by including a "host { }" block.
	LocationHost WorkflowLocation = "host"
)

type Step struct {
	Name    string
	Type    StepType
	Results []string
	Config  map[string]string
}

type Wire struct {
	From     string
	Result   string
	To       string
	Implicit bool
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

// Agent declares a named agent with a command and arguments.
// Steps reference agents by identifier via the "agent" config key.
type Agent struct {
	Name    string
	Command string
	Args    string
}

// DefaultContainerID is the implicit container id for container workflows that do not
// specify an explicit id. All container workflows using the default share one container
// per attempt.
const DefaultContainerID = "_default"

type Workflow struct {
	Name      string
	Location  WorkflowLocation  // host or container
	Steps     map[string]*Step
	Agents    map[string]*Agent // declared agents, keyed by identifier
	Wiring    []Wire
	Collects  []Collect
	EntryStep string
	Config    map[string]string // workflow-level config (e.g. "container.image")
}

// ContainerID returns the container id for this workflow.
// For host workflows this returns an empty string.
// For container workflows it returns the explicit id from the container block,
// or DefaultContainerID when no id is set.
func (w *Workflow) ContainerID() string {
	if w.Location == LocationHost {
		return ""
	}
	if id, ok := w.Config["container.id"]; ok && id != "" {
		return id
	}
	return DefaultContainerID
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

	// Validate agent references
	for name, step := range w.Steps {
		if agentRef, ok := step.Config["agent"]; ok {
			if len(w.Agents) == 0 || w.Agents[agentRef] == nil {
				return fmt.Errorf("workflow %q: step %q references undeclared agent %q", w.Name, name, agentRef)
			}
			if step.Type != StepTypeAgent {
				return fmt.Errorf("workflow %q: step %q references agent %q but is not a prompt step", w.Name, name, agentRef)
			}
		}
	}

	return nil
}

// ResolveAgents expands agent references in steps to agent_command/agent_args config.
// Must be called after Validate to ensure references are valid.
func (w *Workflow) ResolveAgents() {
	for _, step := range w.Steps {
		agentRef, ok := step.Config["agent"]
		if !ok {
			continue
		}
		agent := w.Agents[agentRef]
		if agent == nil {
			continue
		}
		// Only set if not already explicitly overridden at step level
		if _, has := step.Config["agent_command"]; !has && agent.Command != "" {
			step.Config["agent_command"] = agent.Command
		}
		if _, has := step.Config["agent_args"]; !has && agent.Args != "" {
			step.Config["agent_args"] = agent.Args
		}
	}
}

// ValidateLocation checks that step types are compatible with the workflow location.
// workflow_name steps are allowed in both host and container workflows.
func (w *Workflow) ValidateLocation() error {
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
	"agent":         true,
	"results":       true,
	"workflow_name": true,
	"prompt_step":   true,
	"usage_command": true,
	// poll step keys
	"poll":          true,
	"interval":      true,
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
			if strings.HasPrefix(key, "container.") || strings.HasPrefix(key, "host.") {
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
