package domain_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflow_Validate_ValidGraph(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
			},
			"check": {
				Name:    "check",
				Type:    domain.StepTypeScript,
				Results: []string{"pass", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "code", Result: "fail", To: domain.StepAbort},
			{From: "check", Result: "pass", To: domain.StepDone},
			{From: "check", Result: "fail", To: "code"},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	assert.NoError(t, err)
}

func TestWorkflow_Validate_UnwiredResult(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail")
}

func TestWorkflow_Validate_OrphanStep(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
			},
			"orphan": {
				Name:    "orphan",
				Type:    domain.StepTypeScript,
				Results: []string{"done"},
			},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "orphan", Result: "done", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orphan")
}

func TestWorkflow_Validate_NoEntryStep(t *testing.T) {
	wf := &domain.Workflow{
		Name:      "test-workflow",
		Steps:     map[string]*domain.Step{},
		Wiring:    []domain.Wire{},
		EntryStep: "",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entry")
}

func TestWorkflow_NextSteps(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test-workflow",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"check": {Name: "check", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "check", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	next, err := wf.NextSteps("code", "success")
	require.NoError(t, err)
	assert.Equal(t, []string{"check"}, next)

	next, err = wf.NextSteps("check", "pass")
	require.NoError(t, err)
	assert.Equal(t, []string{domain.StepDone}, next)

	next, err = wf.NextSteps("code", "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, next)
}

func TestWorkflow_NextSteps_Fanout(t *testing.T) {
	wf := &domain.Workflow{
		Name: "fanout",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test": {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
			"lint": {Name: "lint", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "pass", To: domain.StepDone},
			{From: "lint", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	next, err := wf.NextSteps("code", "success")
	require.NoError(t, err)
	assert.Equal(t, []string{"test", "lint"}, next)
}

func TestWorkflow_Validate_CollectValid(t *testing.T) {
	wf := &domain.Workflow{
		Name: "parallel",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"lint":  {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"merge": {Name: "merge", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "fail", To: domain.StepAbort},
			{From: "lint", Result: "fail", To: domain.StepAbort},
			{From: "merge", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAll,
				Conditions: []domain.WireCondition{
					{Step: "test", Result: "success"},
					{Step: "lint", Result: "success"},
				},
				To: "merge",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	assert.NoError(t, err)
}

func TestWorkflow_Validate_CollectBadStep(t *testing.T) {
	wf := &domain.Workflow{
		Name: "bad-collect",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode:       domain.CollectAll,
				Conditions: []domain.WireCondition{{Step: "nonexistent", Result: "success"}},
				To:         "code",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestWorkflow_Validate_CollectBadTarget(t *testing.T) {
	wf := &domain.Workflow{
		Name: "bad-target",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode:       domain.CollectAll,
				Conditions: []domain.WireCondition{{Step: "code", Result: "success"}},
				To:         "nonexistent",
			},
		},
		EntryStep: "code",
	}

	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestWorkflow_ValidateConfig_KnownKeys(t *testing.T) {
	wf := &domain.Workflow{
		Name: "known-keys",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Results: []string{"success"},
				Config: map[string]string{
					"prompt":       "do stuff",
					"timeout":      "30m",
					"max_attempts": "3",
				},
			},
		},
	}
	warnings := wf.ValidateConfig()
	assert.Empty(t, warnings)
}

func TestWorkflow_ValidateConfig_UnknownKey(t *testing.T) {
	wf := &domain.Workflow{
		Name: "unknown-key",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Results: []string{"success"},
				Config: map[string]string{
					"prompt":  "do stuff",
					"tiemout": "30m", // typo
				},
			},
		},
	}
	warnings := wf.ValidateConfig()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "tiemout")
	assert.Contains(t, warnings[0], "unrecognized")
}

func TestWorkflow_ValidateLocation_ContainerAllowsWorkflowStep(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "develop",
		Location: domain.LocationContainer,
		Steps: map[string]*domain.Step{
			"dispatch": {
				Name:    "dispatch",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success"},
				Config:  map[string]string{"workflow_name": "implement"},
			},
		},
		EntryStep: "dispatch",
	}
	err := wf.ValidateLocation()
	assert.NoError(t, err)
}

func TestWorkflow_ValidateLocation_HostAllowsWorkflowStep(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "main",
		Location: domain.LocationHost,
		Steps: map[string]*domain.Step{
			"develop": {
				Name:    "develop",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success"},
				Config:  map[string]string{"workflow_name": "develop"},
			},
		},
		EntryStep: "develop",
	}
	err := wf.ValidateLocation()
	assert.NoError(t, err)
}

func TestWorkflow_ValidateLocation_ContainerAllowsAgentAndScript(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "develop",
		Location: domain.LocationContainer,
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"check": {Name: "check", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		EntryStep: "code",
	}
	err := wf.ValidateLocation()
	assert.NoError(t, err)
}

func TestWorkflow_ValidateLocation_EmptyLocationNoEnforcement(t *testing.T) {
	wf := &domain.Workflow{
		Name: "any",
		Steps: map[string]*domain.Step{
			"dispatch": {
				Name:    "dispatch",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success"},
				Config:  map[string]string{"workflow_name": "develop"},
			},
		},
		EntryStep: "dispatch",
	}
	// No Location set — should not enforce
	err := wf.ValidateLocation()
	assert.NoError(t, err)
}

// --- Agent declaration tests ---

func TestWorkflow_Validate_AgentReference(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config:  map[string]string{"prompt": "write code", "agent": "claude"},
			},
		},
		Agents: map[string]*domain.Agent{
			"claude": {Name: "claude", Command: "claude", Args: "-p --verbose"},
		},
		Wiring:    []domain.Wire{{From: "code", Result: "success", To: domain.StepDone}},
		EntryStep: "code",
	}
	err := wf.Validate()
	assert.NoError(t, err)
}

func TestWorkflow_Validate_UndeclaredAgentReference(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config:  map[string]string{"prompt": "write code", "agent": "nonexistent"},
			},
		},
		Agents:    map[string]*domain.Agent{},
		Wiring:    []domain.Wire{{From: "code", Result: "success", To: domain.StepDone}},
		EntryStep: "code",
	}
	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undeclared agent")
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestWorkflow_Validate_AgentOnNonPromptStep(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build": {
				Name:    "build",
				Type:    domain.StepTypeScript,
				Results: []string{"success"},
				Config:  map[string]string{"run": "make build", "agent": "claude"},
			},
		},
		Agents: map[string]*domain.Agent{
			"claude": {Name: "claude", Command: "claude"},
		},
		Wiring:    []domain.Wire{{From: "build", Result: "success", To: domain.StepDone}},
		EntryStep: "build",
	}
	err := wf.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a prompt step")
}

func TestWorkflow_ResolveAgents(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config:  map[string]string{"prompt": "write code", "agent": "claude"},
			},
		},
		Agents: map[string]*domain.Agent{
			"claude": {Name: "claude", Command: "claude", Args: "-p --verbose"},
		},
	}
	wf.ResolveAgents()

	assert.Equal(t, "claude", wf.Steps["code"].Config["agent_command"])
	assert.Equal(t, "-p --verbose", wf.Steps["code"].Config["agent_args"])
}

func TestWorkflow_ResolveAgents_StepOverride(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config: map[string]string{
					"prompt":        "write code",
					"agent":         "claude",
					"agent_command": "custom-claude",
				},
			},
		},
		Agents: map[string]*domain.Agent{
			"claude": {Name: "claude", Command: "claude", Args: "-p --verbose"},
		},
	}
	wf.ResolveAgents()

	// Step-level agent_command should not be overridden
	assert.Equal(t, "custom-claude", wf.Steps["code"].Config["agent_command"])
	// But args should be filled from agent since not set at step level
	assert.Equal(t, "-p --verbose", wf.Steps["code"].Config["agent_args"])
}

func TestWorkflow_ResolveAgents_NoAgentRef(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config:  map[string]string{"prompt": "write code"},
			},
		},
		Agents: map[string]*domain.Agent{
			"claude": {Name: "claude", Command: "claude"},
		},
	}
	wf.ResolveAgents()

	// No agent ref, so agent_command should not be set
	_, hasCmd := wf.Steps["code"].Config["agent_command"]
	assert.False(t, hasCmd)
}

func TestWorkflow_ValidateConfig_AgentKey(t *testing.T) {
	wf := &domain.Workflow{
		Name: "agent-key",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Results: []string{"success"},
				Config: map[string]string{
					"prompt": "do stuff",
					"agent":  "claude",
				},
			},
		},
	}
	warnings := wf.ValidateConfig()
	assert.Empty(t, warnings)
}

func TestWorkflow_ValidateConfig_ContainerPrefix(t *testing.T) {
	wf := &domain.Workflow{
		Name: "container-keys",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Results: []string{"success"},
				Config: map[string]string{
					"prompt":           "do stuff",
					"container.image":  "myimage:v1",
					"container.memory": "4g",
				},
			},
		},
	}
	warnings := wf.ValidateConfig()
	assert.Empty(t, warnings)
}

func TestWorkflow_ContainerID_Default(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "dev",
		Location: domain.LocationContainer,
		Config:   map[string]string{},
	}
	assert.Equal(t, domain.DefaultContainerID, wf.ContainerID())
}

func TestWorkflow_ContainerID_Explicit(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "dev",
		Location: domain.LocationContainer,
		Config:   map[string]string{"container.id": "my-env"},
	}
	assert.Equal(t, "my-env", wf.ContainerID())
}

func TestWorkflow_ContainerID_HostWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "main",
		Location: domain.LocationHost,
		Config:   map[string]string{},
	}
	assert.Equal(t, "", wf.ContainerID())
}

func TestWorkflow_ContainerID_EmptyStringTreatedAsDefault(t *testing.T) {
	wf := &domain.Workflow{
		Name:     "dev",
		Location: domain.LocationContainer,
		Config:   map[string]string{"container.id": ""},
	}
	assert.Equal(t, domain.DefaultContainerID, wf.ContainerID())
}
