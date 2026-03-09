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

func TestParser_WorkflowNameStep(t *testing.T) {
	input := `workflow "orchestrate" {
  step prepare-prompt {
    run     = "bash scripts/prepare.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	assert.Equal(t, "orchestrate", wf.Name)
	assert.Len(t, wf.Steps, 2)

	develop := wf.Steps["develop"]
	require.NotNil(t, develop)
	assert.Equal(t, domain.StepTypeWorkflow, develop.Type)
	assert.Equal(t, "develop", develop.Config["workflow_name"])
	assert.Equal(t, []string{"success", "fail"}, develop.Results)

	preparePrompt := wf.Steps["prepare-prompt"]
	require.NotNil(t, preparePrompt)
	assert.Equal(t, domain.StepTypeScript, preparePrompt.Type)
}

func TestParser_WorkflowNameWithPromptStep(t *testing.T) {
	input := `workflow "orch" {
  step prep {
    run     = "echo hello"
    results = [success, fail]
  }

  step dev {
    workflow_name = "develop"
    prompt_step   = "prep"
    results       = [success, fail]
  }

  prep:success -> dev
  prep:fail    -> abort
  dev:success  -> done
  dev:fail     -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	dev := wf.Steps["dev"]
	require.NotNil(t, dev)
	assert.Equal(t, domain.StepTypeWorkflow, dev.Type)
	assert.Equal(t, "prep", dev.Config["prompt_step"])
}

func TestParser_WorkflowNameWithPromptErrors(t *testing.T) {
	input := `workflow "bad" {
  step both {
    workflow_name = "develop"
    prompt        = "also a prompt"
    results       = [success]
  }
  both:success -> done
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple")
}

func TestParser_WorkflowNameWithRunErrors(t *testing.T) {
	input := `workflow "bad" {
  step both {
    workflow_name = "develop"
    run           = "make test"
    results       = [success]
  }
  both:success -> done
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple")
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

func TestParseForHost_AllowsWorkflowSteps(t *testing.T) {
	input := `workflow "orchestrate" {
  step prepare {
    run     = "bash scripts/prepare.sh"
    results = [success, fail]
  }
  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }
  prepare:success -> develop
  prepare:fail    -> abort
  develop:success -> done
  develop:fail    -> done
}`
	wf, err := dsl.ParseForHost(input)
	require.NoError(t, err)
	assert.Equal(t, domain.LocationHost, wf.Location)
	assert.Equal(t, domain.StepTypeWorkflow, wf.Steps["develop"].Type)
}

func TestParseForContainer_RejectsWorkflowSteps(t *testing.T) {
	input := `workflow "develop" {
  step dispatch {
    workflow_name = "implement"
    results       = [success, fail]
  }
  dispatch:success -> done
  dispatch:fail    -> abort
}`
	_, err := dsl.ParseForContainer(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow_name")
	assert.Contains(t, err.Error(), "host workflows")
}

func TestParseForContainer_AllowsAgentAndScript(t *testing.T) {
	input := `workflow "develop" {
  step code {
    prompt = "write code"
    results = [success, fail]
  }
  step test {
    run = "make test"
    results = [pass, fail]
  }
  code:success -> test
  code:fail -> abort
  test:pass -> done
  test:fail -> code
}`
	wf, err := dsl.ParseForContainer(input)
	require.NoError(t, err)
	assert.Equal(t, domain.LocationContainer, wf.Location)
}

func TestParse_WithoutLocation_NoEnforcement(t *testing.T) {
	input := `workflow "any" {
  step dispatch {
    workflow_name = "develop"
    results       = [success]
  }
  dispatch:success -> done
}`
	// Plain Parse (no location) should not enforce location constraints
	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, domain.WorkflowLocation(""), wf.Location)
}
