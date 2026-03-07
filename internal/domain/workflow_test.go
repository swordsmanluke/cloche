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

func TestWorkflow_ValidateConfig_AgentArgsPrefix(t *testing.T) {
	wf := &domain.Workflow{
		Name: "agent-args-keys",
		Steps: map[string]*domain.Step{
			"code": {
				Name:    "code",
				Results: []string{"success"},
				Config: map[string]string{
					"prompt":            "do stuff",
					"agent_args.claude": "-p --verbose",
					"agent_args.gemini": "--model gemini-2.5-pro",
				},
			},
		},
	}
	warnings := wf.ValidateConfig()
	assert.Empty(t, warnings)
}
