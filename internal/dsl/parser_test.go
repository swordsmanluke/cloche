package dsl_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParser_FullWorkflow(t *testing.T) {
	input := `workflow "implement-feature" {
  step code {
    prompt = file("prompts/implement.md")
    results = [success, fail, retry_with_feedback]
  }

  step check {
    run = "make test && make lint"
    results = [pass, fail]
  }

  code:success -> check
  code:fail -> abort
  code:retry_with_feedback -> code

  check:pass -> done
  check:fail -> code
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	assert.Equal(t, "implement-feature", wf.Name)
	assert.Len(t, wf.Steps, 2)

	code := wf.Steps["code"]
	require.NotNil(t, code)
	assert.Equal(t, domain.StepTypeAgent, code.Type)
	assert.Equal(t, []string{"success", "fail", "retry_with_feedback"}, code.Results)
	assert.Equal(t, `file("prompts/implement.md")`, code.Config["prompt"])

	check := wf.Steps["check"]
	require.NotNil(t, check)
	assert.Equal(t, domain.StepTypeScript, check.Type)
	assert.Equal(t, "make test && make lint", check.Config["run"])

	assert.Len(t, wf.Wiring, 5)
	assert.Equal(t, "code", wf.EntryStep)
}

func TestParser_MinimalWorkflow(t *testing.T) {
	input := `workflow "simple" {
  step build {
    run = "make build"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "simple", wf.Name)
	assert.Equal(t, "build", wf.EntryStep)
}

func TestParser_SyntaxError(t *testing.T) {
	input := `workflow { }`
	_, err := dsl.Parse(input)
	assert.Error(t, err)
}

func TestParser_ContainerBlock(t *testing.T) {
	input := `workflow "test" {
  step code {
    prompt = "do something"
    container {
      image = "cloche/agent:latest"
      network_allow = ["docs.python.org", "internal.example.com"]
    }
    results = [success]
  }
  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	code := wf.Steps["code"]
	assert.Equal(t, "cloche/agent:latest", code.Config["container.image"])
	assert.Equal(t, "docs.python.org,internal.example.com", code.Config["container.network_allow"])
}

func TestParser_InfersTypeFromContent(t *testing.T) {
	input := `workflow "infer" {
  step build {
    run = "make build"
    results = [success]
  }
  step code {
    prompt = "write code"
    results = [success]
  }
  build:success -> code
  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, domain.StepTypeScript, wf.Steps["build"].Type)
	assert.Equal(t, domain.StepTypeAgent, wf.Steps["code"].Type)
}

func TestParser_AmbiguousStepType(t *testing.T) {
	input := `workflow "bad" {
  step both {
    run = "make test"
    prompt = "also a prompt"
    results = [success]
  }
  both:success -> done
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "both")
}

func TestParser_NoStepType(t *testing.T) {
	input := `workflow "bad" {
  step neither {
    results = [success]
  }
  neither:success -> done
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "neither")
}
