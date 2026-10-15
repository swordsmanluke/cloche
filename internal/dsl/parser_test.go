package dsl_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
)

func TestParser_FullWorkflow(t *testing.T) {
	input := `workflow implement-feature {
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
	assert.Equal(t, []string{"success", "fail", "retry_with_feedback", "timeout", "token-limit"}, code.Results)
	assert.Equal(t, `file("prompts/implement.md")`, code.Config["prompt"])

	check := wf.Steps["check"]
	require.NotNil(t, check)
	assert.Equal(t, domain.StepTypeScript, check.Type)
	assert.Equal(t, "make test && make lint", check.Config["run"])

	assert.Len(t, wf.Wiring, 9)
	assert.Equal(t, "code", wf.EntryStep)
}

func TestParser_MinimalWorkflow(t *testing.T) {
	input := `workflow simple {
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

func TestParser_WorkflowLevelIntentTrackingOff(t *testing.T) {
	input := `workflow simple {
  intent_tracking = false

  step build {
    run = "make build"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "false", wf.Config["intent_tracking"])
}

func TestParser_WorkflowLevelIntentTrackingInvalid(t *testing.T) {
	input := `workflow simple {
  intent_tracking = "off"

  step build {
    run = "make build"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
}

func TestParser_StepLevelIntentTrackingOff(t *testing.T) {
	input := `workflow simple {
  step build {
    run = "make build"
    intent_tracking = false
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "false", wf.Steps["build"].Config["intent_tracking"])
}

func TestParser_StepLevelIntentTrackingInvalid(t *testing.T) {
	input := `workflow simple {
  step build {
    run = "make build"
    intent_tracking = "nope"
    results = [success, fail]
  }

  build:success -> done
  build:fail -> abort
}`

	_, err := dsl.Parse(input)
	assert.Error(t, err)
}

func TestParser_SyntaxError(t *testing.T) {
	input := `workflow { }`
	_, err := dsl.Parse(input)
	assert.Error(t, err)
}

func TestParser_ContainerBlock(t *testing.T) {
	input := `workflow test {
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
	input := `workflow with-image {
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
	input := `workflow full-config {
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
	input := `workflow no-container {
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

func TestParser_ContainerBlockWithID(t *testing.T) {
	input := `workflow dev {
  container {
    id = "dev-env"
    image = "myimage:latest"
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
	assert.Equal(t, "dev-env", wf.Config["container.id"])
	assert.Equal(t, "myimage:latest", wf.Config["container.image"])
	assert.Equal(t, "4g", wf.Config["container.memory"])
}

func TestParser_ContainerBlockIDOnly(t *testing.T) {
	input := `workflow test {
  container {
    id = "dev-env"
  }

  step code {
    prompt = "write code"
    results = [success]
  }

  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	assert.Equal(t, "dev-env", wf.Config["container.id"])
	assert.Empty(t, wf.Config["container.image"])
}

func TestParser_InfersTypeFromContent(t *testing.T) {
	input := `workflow infer {
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
	input := `workflow bad {
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
	input := `workflow bad {
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
	input := `workflow parallel {
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
	input := `workflow main {
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

	assert.Equal(t, "main", wf.Name)
	assert.Len(t, wf.Steps, 2)

	develop := wf.Steps["develop"]
	require.NotNil(t, develop)
	assert.Equal(t, domain.StepTypeWorkflow, develop.Type)
	assert.Equal(t, "develop", develop.Config["workflow_name"])
	assert.Equal(t, []string{"success", "fail", "timeout", "token-limit"}, develop.Results)

	preparePrompt := wf.Steps["prepare-prompt"]
	require.NotNil(t, preparePrompt)
	assert.Equal(t, domain.StepTypeScript, preparePrompt.Type)
}

func TestParser_WorkflowNameWithPromptStep(t *testing.T) {
	input := `workflow orch {
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
	input := `workflow bad {
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
	input := `workflow bad {
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
	input := `workflow race {
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
	input := `workflow main {
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

func TestParseForContainer_AllowsWorkflowSteps(t *testing.T) {
	input := `workflow develop {
  step dispatch {
    workflow_name = "implement"
    results       = [success, fail]
  }
  dispatch:success -> done
  dispatch:fail    -> abort
}`
	wf, err := dsl.ParseForContainer(input)
	require.NoError(t, err)
	assert.Equal(t, domain.LocationContainer, wf.Location)
	assert.Equal(t, domain.StepTypeWorkflow, wf.Steps["dispatch"].Type)
}

func TestParseForContainer_AllowsAgentAndScript(t *testing.T) {
	input := `workflow develop {
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

func TestParser_WireNoMappings(t *testing.T) {
	input := `workflow simple {
  step a {
    run = "echo a"
    results = [success]
  }
  a:success -> done
}`
	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	require.Len(t, wf.Wiring, 3)
}

func TestParser_WireBracketSyntaxIsError(t *testing.T) {
	input := `workflow bad {
  step a {
    run = "echo a"
    results = [success]
  }
  step b {
    run = "echo b"
    results = [success]
  }
  a:success -> b [X=output.y]
  b:success -> done
}`
	_, err := dsl.Parse(input)
	require.Error(t, err)
}

func TestParse_WithoutLocation_NoEnforcement(t *testing.T) {
	input := `workflow any {
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

// --- ParseAllForHost tests ---

func TestParseAllForHost_SingleWorkflow(t *testing.T) {
	input := `workflow main {
  step greet {
    run     = "echo hi"
    results = [success, fail]
  }
  greet:success -> done
  greet:fail    -> abort
}`
	workflows, err := dsl.ParseAllForHost(input)
	require.NoError(t, err)
	require.Len(t, workflows, 1)

	wf, ok := workflows["main"]
	require.True(t, ok)
	assert.Equal(t, "main", wf.Name)
	assert.Equal(t, domain.LocationHost, wf.Location)
}

func TestParseAllForHost_MultipleWorkflows(t *testing.T) {
	input := `workflow list-tasks {
  step fetch {
    run     = "bash scripts/list-tasks.sh"
    results = [success, fail]
  }
  fetch:success -> done
  fetch:fail    -> abort
}

workflow main {
  step prepare {
    run     = "echo preparing"
    results = [success, fail]
  }
  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }
  prepare:success -> develop
  prepare:fail    -> abort
  develop:success -> done
  develop:fail    -> abort
}

workflow post-merge {
  step cleanup {
    run     = "echo cleanup"
    results = [success, fail]
  }
  cleanup:success -> done
  cleanup:fail    -> abort
}`

	workflows, err := dsl.ParseAllForHost(input)
	require.NoError(t, err)
	require.Len(t, workflows, 3)

	_, ok := workflows["list-tasks"]
	assert.True(t, ok, "should have list-tasks workflow")

	_, ok = workflows["main"]
	assert.True(t, ok, "should have main workflow")

	_, ok = workflows["post-merge"]
	assert.True(t, ok, "should have post-merge workflow")
}

func TestParseAllForHost_DuplicateName(t *testing.T) {
	input := `workflow main {
  step a {
    run = "echo a"
    results = [success]
  }
  a:success -> done
}
workflow main {
  step b {
    run = "echo b"
    results = [success]
  }
  b:success -> done
}`

	_, err := dsl.ParseAllForHost(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate workflow name")
}

func TestParseAllForHost_Empty(t *testing.T) {
	_, err := dsl.ParseAllForHost("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workflows found")
}

func TestParseAllForHost_AllHostLocation(t *testing.T) {
	input := `workflow list-tasks {
  step fetch {
    run = "echo fetch"
    results = [success]
  }
  fetch:success -> done
}
workflow main {
  step work {
    run = "echo work"
    results = [success]
  }
  work:success -> done
}`

	workflows, err := dsl.ParseAllForHost(input)
	require.NoError(t, err)
	for name, wf := range workflows {
		assert.Equal(t, domain.LocationHost, wf.Location, "workflow %q should have host location", name)
	}
}

func TestParseAll_HostBlockSetsLocation(t *testing.T) {
	input := `workflow main {
  host {
    agent_command = "claude"
  }
  step work {
    run = "echo work"
    results = [success]
  }
  work:success -> done
}`
	workflows, err := dsl.ParseAll(input)
	require.NoError(t, err)
	assert.Equal(t, domain.LocationHost, workflows["main"].Location)
}

func TestParseAll_NoHostBlockDefaultsToContainer(t *testing.T) {
	input := `workflow build {
  step compile {
    run = "make"
    results = [success]
  }
  compile:success -> done
}`
	workflows, err := dsl.ParseAll(input)
	require.NoError(t, err)
	assert.Equal(t, domain.LocationContainer, workflows["build"].Location)
}

func TestParse_RejectsHostAndContainerBlocks(t *testing.T) {
	input := `workflow bad {
  host {}
  container {
    image = "foo"
  }
  step work {
    run = "echo work"
    results = [success]
  }
  work:success -> done
}`
	_, err := dsl.ParseAll(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both \"host\" and \"container\"")
}

// --- Agent declaration tests ---

func TestParser_AgentDeclaration(t *testing.T) {
	input := `workflow develop {
  agent claude {
    command = "claude"
    args = "-p --output-format stream-json"
  }

  step code {
    prompt = "write code"
    agent = claude
    results = [success, fail]
  }

  code:success -> done
  code:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Agents, 1)
	agent := wf.Agents["claude"]
	require.NotNil(t, agent)
	assert.Equal(t, "claude", agent.Name)
	assert.Equal(t, "claude", agent.Command)
	assert.Equal(t, "-p --output-format stream-json", agent.Args)

	code := wf.Steps["code"]
	require.NotNil(t, code)
	assert.Equal(t, "claude", code.Config["agent"])
}

func TestParser_MultipleAgents(t *testing.T) {
	input := `workflow develop {
  agent claude {
    command = "claude"
    args = "-p --verbose"
  }

  agent codex {
    command = "codex"
    args = "--full-auto"
  }

  step implement {
    prompt = "implement the feature"
    agent = claude
    results = [success, fail]
  }

  step review {
    prompt = "review the code"
    agent = codex
    results = [success, fail]
  }

  implement:success -> review
  implement:fail -> abort
  review:success -> done
  review:fail -> implement
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	require.Len(t, wf.Agents, 2)
	assert.Equal(t, "claude", wf.Agents["claude"].Command)
	assert.Equal(t, "codex", wf.Agents["codex"].Command)

	assert.Equal(t, "claude", wf.Steps["implement"].Config["agent"])
	assert.Equal(t, "codex", wf.Steps["review"].Config["agent"])
}

func TestParser_AgentCommandOnly(t *testing.T) {
	input := `workflow simple {
  agent ollama {
    command = "ollama"
  }

  step code {
    prompt = "write code"
    agent = ollama
    results = [success]
  }

  code:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	agent := wf.Agents["ollama"]
	require.NotNil(t, agent)
	assert.Equal(t, "ollama", agent.Command)
	assert.Equal(t, "", agent.Args)
}

func TestParser_AgentMissingCommand(t *testing.T) {
	input := `workflow bad {
  agent nocommand {
    args = "--verbose"
  }

  step code {
    prompt = "write"
    results = [success]
  }

  code:success -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command")
}

func TestParser_DuplicateAgent(t *testing.T) {
	input := `workflow bad {
  agent claude {
    command = "claude"
  }
  agent claude {
    command = "claude2"
  }

  step code {
    prompt = "write"
    results = [success]
  }

  code:success -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate agent")
}

func TestParser_AgentUnknownField(t *testing.T) {
	input := `workflow bad {
  agent claude {
    command = "claude"
    unknown = "value"
  }

  step code {
    prompt = "write"
    results = [success]
  }

  code:success -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent field")
}

func TestParser_NumericMaxAttempts(t *testing.T) {
	input := `workflow retry {
  step code {
    prompt = "write code"
    max_attempts = 3
    results = [success, fail, give-up]
  }
  code:success -> done
  code:fail -> code
  code:give-up -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	code := wf.Steps["code"]
	require.NotNil(t, code)
	assert.Equal(t, "3", code.Config["max_attempts"])
}

func TestParser_StringMaxAttempts_Rejected(t *testing.T) {
	input := `workflow retry {
  step code {
    prompt = "write code"
    max_attempts = "3"
    results = [success, fail, give-up]
  }
  code:success -> done
  code:fail -> code
  code:give-up -> abort
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_attempts must be a numeric value")
}

func TestParser_PollStep_Valid(t *testing.T) {
	input := `workflow review {
  host {}
  step create-pr {
    run     = "gh pr create"
    results = [success, fail]
  }
  step code-review {
    poll     = "scripts/check-pr-review.sh"
    interval = "5m"
    results  = [approved, fix]
  }
  step merge {
    run = "gh pr merge"
    results = [success]
  }
  create-pr:success -> code-review
  create-pr:fail    -> abort
  code-review:approved -> merge
  code-review:fix      -> abort
  merge:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	review := wf.Steps["code-review"]
	require.NotNil(t, review)
	assert.Equal(t, domain.StepTypePoll, review.Type)
	assert.Equal(t, "scripts/check-pr-review.sh", review.Config["poll"])
	assert.Equal(t, "5m", review.Config["interval"])

	// Implicit "timeout" result should be added.
	assert.Contains(t, review.Results, "timeout")
	// Explicit results preserved.
	assert.Contains(t, review.Results, "approved")
	assert.Contains(t, review.Results, "fix")

	// Implicit timeout -> abort wire should be added.
	foundTimeoutWire := false
	for _, w := range wf.Wiring {
		if w.From == "code-review" && w.Result == "timeout" && w.To == domain.StepAbort {
			foundTimeoutWire = true
			break
		}
	}
	assert.True(t, foundTimeoutWire, "expected implicit timeout->abort wire")
}

func TestParser_PollStep_WithTimeout(t *testing.T) {
	input := `workflow review {
  host {}
  step wait {
    poll     = "scripts/check.sh"
    interval = "10m"
    timeout  = "48h"
    results  = [approved]
  }
  wait:approved -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	step := wf.Steps["wait"]
	require.NotNil(t, step)
	assert.Equal(t, domain.StepTypePoll, step.Type)
	assert.Equal(t, "48h", step.Config["timeout"])
}

func TestParser_PollStep_ExplicitTimeoutWire(t *testing.T) {
	input := `workflow review {
  host {}
  step code-review {
    poll     = "scripts/check.sh"
    interval = "10m"
    timeout  = "48h"
    results  = [approved, fix, timeout]
  }
  step escalate {
    run = "echo escalating"
    results = [success]
  }
  code-review:approved -> done
  code-review:fix -> abort
  code-review:timeout -> escalate
  escalate:success -> done
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)

	review := wf.Steps["code-review"]
	require.NotNil(t, review)
	assert.Equal(t, domain.StepTypePoll, review.Type)
	assert.Equal(t, "48h", review.Config["timeout"])

	// Only one timeout wire should exist (the explicit one, no implicit abort added).
	timeoutWires := 0
	for _, w := range wf.Wiring {
		if w.From == "code-review" && w.Result == "timeout" {
			timeoutWires++
		}
	}
	assert.Equal(t, 1, timeoutWires, "expected exactly one timeout wire (the explicit one)")

	// Explicit wire should go to escalate, not abort.
	for _, w := range wf.Wiring {
		if w.From == "code-review" && w.Result == "timeout" {
			assert.Equal(t, "escalate", w.To)
		}
	}
}

func TestParser_PollStep_MissingInterval(t *testing.T) {
	input := `workflow review {
  host {}
  step wait {
    poll    = "scripts/check.sh"
    results = [approved]
  }
  wait:approved -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval")
}

func TestParser_PollStep_MissingPoll(t *testing.T) {
	// A step with only interval and no type keyword should fail with a clear message.
	input := `workflow review {
  host {}
  step wait {
    interval = "5m"
    results  = [approved]
  }
  wait:approved -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must have exactly one")
}

func TestParser_PollStep_InvalidInterval(t *testing.T) {
	input := `workflow review {
  host {}
  step wait {
    poll     = "scripts/check.sh"
    interval = "notaduration"
    results  = [approved]
  }
  wait:approved -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval")
}

func TestParser_PollStep_ConflictsWithRun(t *testing.T) {
	input := `workflow review {
  host {}
  step wait {
    poll     = "scripts/check.sh"
    run      = "echo hi"
    interval = "5m"
    results  = [approved]
  }
  wait:approved -> done
}`

	_, err := dsl.Parse(input)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple")
}

func TestParser_PollStep_Validate(t *testing.T) {
	// A valid poll step workflow should pass Validate().
	input := `workflow review {
  host {}
  step review {
    poll     = "scripts/check.sh"
    interval = "5m"
    results  = [approved, fail]
  }

  review:approved -> done
  review:fail -> abort
}`

	wf, err := dsl.Parse(input)
	require.NoError(t, err)
	require.NoError(t, wf.Validate())
}

func TestParseContract(t *testing.T) {
	wf, err := dsl.ParseForContainer(`workflow code-review {
  contract {
    kv:  child_branch
    kv:  task_id
    env: REVIEW_MODEL
    out: review_path
  }
  step review { run = "true" results = [success, fail] }
  review:success -> done
  review:fail -> abort
}`)
	if err != nil {
		t.Fatal(err)
	}
	c := wf.Contract
	if c == nil || len(c.KV) != 2 || c.KV[1] != "task_id" || len(c.Env) != 1 || c.Env[0] != "REVIEW_MODEL" || len(c.Out) != 1 || c.Out[0] != "review_path" {
		t.Fatalf("contract = %+v", c)
	}
}

func TestParseContractErrors(t *testing.T) {
	if _, err := dsl.ParseForContainer(`workflow w { contract { foo: x } step a { run = "true" results = [success] } a:success -> done }`); err == nil {
		t.Fatal("expected error for unknown contract field")
	}
	if _, err := dsl.ParseForContainer(`workflow w { contract { kv: a } contract { kv: b } step a { run = "true" results = [success] } a:success -> done }`); err == nil {
		t.Fatal("expected error for duplicate contract")
	}
}

func TestParseWith(t *testing.T) {
	wf, err := dsl.ParseForHost(`workflow w {
  host {}
  step review {
    workflow_name = "code-review"
    with {
      task_id          from ticket_id
      env.REVIEW_MODEL = "opus"
      env.JIRA_URL     from jira_url
      feedback_path    from out.review_path
    }
    results = [success, fail]
  }
  review:success -> done
  review:fail -> abort
}`)
	if err != nil {
		t.Fatal(err)
	}
	step := wf.Steps["review"]
	if step.Type != domain.StepTypeWorkflow || step.Config["workflow_name"] != "code-review" {
		t.Fatalf("step = %+v", step)
	}
	want := []domain.WithEntry{
		{Key: "task_id", Source: domain.WithKV, Value: "ticket_id"},
		{Key: "REVIEW_MODEL", Env: true, Source: domain.WithLiteral, Value: "opus"},
		{Key: "JIRA_URL", Env: true, Source: domain.WithKV, Value: "jira_url"},
		{Key: "feedback_path", Source: domain.WithOut, Value: "review_path"},
	}
	if len(step.With) != len(want) {
		t.Fatalf("with = %+v", step.With)
	}
	for i := range want {
		if step.With[i] != want[i] {
			t.Errorf("with[%d] = %+v, want %+v", i, step.With[i], want[i])
		}
	}
}

func TestParseWithErrors(t *testing.T) {
	for _, body := range []string{
		`a b`,
		`a from`,
		`env.X from out.y`,
		`a = b`,
	} {
		src := `workflow w { host {} step s { workflow_name = "x" with { ` + body + ` } results = [success] } s:success -> done }`
		if _, err := dsl.ParseForHost(src); err == nil {
			t.Errorf("with { %s }: expected error", body)
		}
	}
}

func TestParseBinds(t *testing.T) {
	f, err := dsl.ParseFile(`
bind code-review = ruby-review
bind! lint = standardrb-lint
bind {
  develop = tdd-develop
  list-tasks = jira-tasks:list-tasks
}
bind! {
  deploy = safe-deploy
}

workflow security-path {
  host {}
  bind code-review = sec-review
  step review { workflow_name = "code-review" results = [success, fail] }
  step quick {
    workflow_name = "code-review"
    bind code-review = quick-review
    bind! { lint = x }
    results = [success, fail]
  }
  review:success -> quick
  review:fail -> abort
  quick:success -> done
  quick:fail -> abort
}`, dsl.WithSourceFile("/p/.cloche/security.cloche"))
	if err != nil {
		t.Fatal(err)
	}
	wantFile := []domain.Bind{
		{Slot: "code-review", Target: "ruby-review"},
		{Slot: "lint", Target: "standardrb-lint", Force: true},
		{Slot: "develop", Target: "tdd-develop"},
		{Slot: "list-tasks", Target: "jira-tasks:list-tasks"},
		{Slot: "deploy", Target: "safe-deploy", Force: true},
	}
	if fmt.Sprint(f.Binds) != fmt.Sprint(wantFile) {
		t.Fatalf("file binds = %+v", f.Binds)
	}
	wf := f.Workflows["security-path"]
	if wf.SourceFile != "/p/.cloche/security.cloche" {
		t.Errorf("SourceFile = %q", wf.SourceFile)
	}
	if len(wf.Binds) != 1 || wf.Binds[0] != (domain.Bind{Slot: "code-review", Target: "sec-review"}) {
		t.Errorf("workflow binds = %+v", wf.Binds)
	}
	q := wf.Steps["quick"]
	if len(q.Binds) != 2 || q.Binds[0].Target != "quick-review" || !q.Binds[1].Force {
		t.Errorf("step binds = %+v", q.Binds)
	}
	if q.Type != domain.StepTypeWorkflow {
		t.Errorf("step type = %v", q.Type)
	}
}

func TestParseBindErrors(t *testing.T) {
	for _, src := range []string{
		"bind x\nworkflow w { host {} step a { run = \"true\" results = [success] } a:success -> done }",
		"bind { x = }\nworkflow w { host {} step a { run = \"true\" results = [success] } a:success -> done }",
		"bind x = y:\nworkflow w { host {} step a { run = \"true\" results = [success] } a:success -> done }",
	} {
		if _, err := dsl.ParseFile(src); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}
