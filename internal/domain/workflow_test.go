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
