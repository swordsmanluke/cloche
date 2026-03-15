package host

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestBuildPreloadedResults_LinearWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build":  {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":   {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
			"deploy": {Name: "deploy", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: "deploy"},
			{From: "test", Result: "fail", To: domain.StepAbort},
			{From: "deploy", Result: "success", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "success"},
			{StepName: "test", Result: "fail"},
		},
	}

	// Resume from test: build should be preloaded
	preloaded := buildPreloadedResults(run, wf, "test")
	assert.Equal(t, map[string]string{"build": "success"}, preloaded)
}

func TestBuildPreloadedResults_ResumeFromFirst(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "build", Result: "fail", To: domain.StepAbort},
			{From: "test", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "fail"},
		},
	}

	// Resume from build (entry step): nothing should be preloaded
	preloaded := buildPreloadedResults(run, wf, "build")
	assert.Empty(t, preloaded)
}

func TestBuildPreloadedResults_MultipleCompleted(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"b": {Name: "b", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"c": {Name: "c", Type: domain.StepTypeScript, Results: []string{"ok", "fail"}},
			"d": {Name: "d", Type: domain.StepTypeScript, Results: []string{"ok"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "b"},
			{From: "b", Result: "ok", To: "c"},
			{From: "c", Result: "ok", To: "d"},
			{From: "c", Result: "fail", To: domain.StepAbort},
			{From: "d", Result: "ok", To: domain.StepDone},
		},
		EntryStep: "a",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "a", Result: "ok"},
			{StepName: "b", Result: "ok"},
			{StepName: "c", Result: "fail"},
		},
	}

	// Resume from c: a and b should be preloaded
	preloaded := buildPreloadedResults(run, wf, "c")
	assert.Equal(t, map[string]string{"a": "ok", "b": "ok"}, preloaded)
}

func TestBuildPreloadedResults_SkipsErrorResults(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: domain.StepDone},
			{From: "test", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "build",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "error"}, // error results should be excluded
			{StepName: "test", Result: "fail"},
		},
	}

	// Resume from build: nothing should be preloaded because build had "error"
	preloaded := buildPreloadedResults(run, wf, "build")
	assert.Empty(t, preloaded)
}

func TestFindFirstFailedStep(t *testing.T) {
	tests := []struct {
		name     string
		run      *domain.Run
		expected string
	}{
		{
			name: "finds first fail",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
					{StepName: "test", Result: "fail"},
				},
			},
			expected: "test",
		},
		{
			name: "finds error result",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
					{StepName: "deploy", Result: "error"},
				},
			},
			expected: "deploy",
		},
		{
			name: "no failures",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
				},
			},
			expected: "",
		},
		{
			name:     "empty executions",
			run:      &domain.Run{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.run.FindFirstFailedStep()
			assert.Equal(t, tt.expected, result)
		})
	}
}
