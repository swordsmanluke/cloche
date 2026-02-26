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

func TestParser_WorkflowContainerBlock(t *testing.T) {
	input := `workflow "with-image" {
  container {
    image = "myregistry/myimage:v2"
  }

  step code {
    prompt = "write code"
    results = [success, fail]
  }

  code:success -> done
  code:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "with-image", wf.Name)
	assert.Equal(t, "myregistry/myimage:v2", wf.Config["container.image"])
}

func TestParser_WorkflowContainerBlockMultipleFields(t *testing.T) {
	input := `workflow "full-config" {
  container {
    image = "myregistry/myimage:v2"
    memory = "4g"
  }

  step code {
    prompt = "write code"
    results = [success]
  }

  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "myregistry/myimage:v2", wf.Config["container.image"])
	assert.Equal(t, "4g", wf.Config["container.memory"])
}

func TestParser_WorkflowWithoutContainerBlock(t *testing.T) {
	input := `workflow "no-container" {
  step code {
    prompt = "write code"
    results = [success]
  }

  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Empty(t, wf.Config["container.image"])
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

func TestParser_CollectAll(t *testing.T) {
	input := `workflow "parallel" {
  step code {
    prompt = "write code"
    results = [success]
  }
  step test {
    run = "make test"
    results = [success, fail]
  }
  step lint {
    run = "make lint"
    results = [success, fail]
  }
  step merge {
    run = "echo merged"
    results = [success]
  }

  code:success -> test
  code:success -> lint
  test:fail -> abort
  lint:fail -> abort
  collect all(test:success, lint:success) -> merge
  merge:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Collects, 1)
	c := wf.Collects[0]
	assert.Equal(t, domain.CollectAll, c.Mode)
	assert.Equal(t, "merge", c.To)
	require.Len(t, c.Conditions, 2)
	assert.Equal(t, "test", c.Conditions[0].Step)
	assert.Equal(t, "success", c.Conditions[0].Result)
	assert.Equal(t, "lint", c.Conditions[1].Step)
	assert.Equal(t, "success", c.Conditions[1].Result)
}

func TestParser_CollectAny(t *testing.T) {
	input := `workflow "race" {
  step fast {
    run = "echo fast"
    results = [success]
  }
  step slow {
    run = "sleep 1 && echo slow"
    results = [success]
  }
  step next {
    run = "echo next"
    results = [success]
  }

  collect any(fast:success, slow:success) -> next
  next:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Collects, 1)
	assert.Equal(t, domain.CollectAny, wf.Collects[0].Mode)
}
