package dsl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMutatorAddStep(t *testing.T) {
	input := `workflow "develop" {
  step test {
    run = "make test"
    results = [success, fail]
  }

  test:success -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.AddStep(input, StepDef{
		Name:    "security-scan",
		Type:    "script",
		Config:  map[string]string{"run": `"gosec ./..."`},
		Results: []string{"success", "fail"},
	})
	require.NoError(t, err)
	assert.Contains(t, result, "step security-scan")
	assert.Contains(t, result, `run = "gosec ./..."`)
	assert.Contains(t, result, "results = [success, fail]")

	// Verify the result still parses
	wf, err := Parse(result)
	require.NoError(t, err)
	assert.Contains(t, wf.Steps, "security-scan")
	assert.Equal(t, "script", string(wf.Steps["security-scan"].Type))
}

func TestMutatorAddStepAgent(t *testing.T) {
	input := `workflow "develop" {
  step test {
    run = "make test"
    results = [success, fail]
  }

  test:success -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.AddStep(input, StepDef{
		Name:    "review",
		Type:    "agent",
		Config:  map[string]string{"prompt": `file("prompts/review.md")`, "max_attempts": `"2"`},
		Results: []string{"success", "fail", "give-up"},
	})
	require.NoError(t, err)
	assert.Contains(t, result, "step review")
	assert.Contains(t, result, `prompt = file("prompts/review.md")`)
	assert.Contains(t, result, `max_attempts = "2"`)

	wf, err := Parse(result)
	require.NoError(t, err)
	assert.Contains(t, wf.Steps, "review")
	assert.Equal(t, "agent", string(wf.Steps["review"].Type))
}

func TestMutatorAddWiring(t *testing.T) {
	input := `workflow "develop" {
  step test {
    run = "make test"
    results = [success, fail]
  }

  step scan {
    run = "gosec ./..."
    results = [success, fail]
  }

  test:success -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.AddWiring(input, []WireDef{
		{From: "test", Result: "success", To: "scan"},
		{From: "scan", Result: "fail", To: "abort"},
		{From: "scan", Result: "success", To: "done"},
	})
	require.NoError(t, err)
	assert.Contains(t, result, "test:success -> scan")
	assert.Contains(t, result, "scan:fail -> abort")
	assert.Contains(t, result, "scan:success -> done")

	wf, err := Parse(result)
	require.NoError(t, err)
	// Original 2 wires + 3 new = 5
	assert.Len(t, wf.Wiring, 5)
}

func TestMutatorUpdateCollect(t *testing.T) {
	input := `workflow "develop" {
  step test {
    run = "make test"
    results = [success, fail]
  }

  step lint {
    run = "golint ./..."
    results = [success, fail]
  }

  step scan {
    run = "gosec ./..."
    results = [success, fail]
  }

  step fmt {
    run = "gofmt -l ."
    results = [success, fail]
  }

  test:success -> lint
  test:success -> scan
  collect all(lint:success, scan:success) -> done
  test:fail -> abort
}`

	m := &Mutator{}
	result, err := m.UpdateCollect(input, CollectAddition{
		CollectTarget: "done",
		Step:          "fmt",
		Result:        "success",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "fmt:success")

	wf, err := Parse(result)
	require.NoError(t, err)
	require.Len(t, wf.Collects, 1)
	assert.Len(t, wf.Collects[0].Conditions, 3)
}
