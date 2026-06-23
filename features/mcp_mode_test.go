package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

func init() { registerScenarios(initMCPModeScenarios) }

// mcpModeCtx holds state across steps within a single MCP mode scenario.
type mcpModeCtx struct {
	// L1: config loading
	configContent string
	agentMode     string

	// L2: MCP session
	runID        string
	sessionToken string
	nextPrompt   string
	stepName     string
	stepResult   string
	stepOutput   string
	outputTokens int

	// L3: end-to-end execution
	runResult        string
	claudePInvoked   bool
	mcpDispatched    bool
	sessionTokensMap map[string]string // stepName → session token used
	failureReason    string
}

func (s *mcpModeCtx) reset() {
	*s = mcpModeCtx{}
}

func initMCPModeScenarios(ctx *godog.ScenarioContext) {
	s := &mcpModeCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// ─── L1: Configuration ───────────────────────────────────────────────────

	ctx.Step(`^a config\.toml with agent mode "([^"]*)"$`, s.aConfigTOMLWithAgentMode)
	ctx.Step(`^a config\.toml with no agent mode setting$`, s.aConfigTOMLWithNoAgentMode)
	ctx.Step(`^the mcp config is loaded$`, s.theMCPConfigIsLoaded)
	ctx.Step(`^the loaded config has agent mode "([^"]*)"$`, s.theLoadedConfigHasAgentMode)

	// ─── L2: MCP server tool surface ─────────────────────────────────────────

	ctx.Step(`^the daemon is running in MCP mode for run "([^"]*)"$`, s.theDaemonIsRunningInMCPModeForRun)
	ctx.Step(`^the MCP client calls "init" for run "([^"]*)"$`, s.theMCPClientCallsInitForRun)
	ctx.Step(`^the init response contains a non-empty session token$`, s.theInitResponseContainsANonEmptySessionToken)

	ctx.Step(`^an MCP session for run "([^"]*)" has a pending prompt step with prompt "([^"]*)"$`, s.anMCPSessionHasPendingPrompt)
	ctx.Step(`^the MCP client calls "next" with the session token$`, s.theMCPClientCallsNext)
	ctx.Step(`^the next response contains the prompt text "([^"]*)"$`, s.theNextResponseContainsPromptText)

	ctx.Step(`^an MCP session for run "([^"]*)" is waiting for step "([^"]*)" to complete$`, s.anMCPSessionWaitingForStep)
	ctx.Step(`^the MCP client calls "submit-result" with result "([^"]*)", output "([^"]*)", and (\d+) output tokens$`, s.theMCPClientCallsSubmitResult)
	ctx.Step(`^step "([^"]*)" in run "([^"]*)" is marked complete with result "([^"]*)"$`, s.stepIsMarkedCompleteWithResult)
	ctx.Step(`^(\d+) output tokens are recorded for step "([^"]*)" in run "([^"]*)"$`, s.outputTokensRecordedForStep)

	// ─── L3: End-to-end execution ─────────────────────────────────────────────

	ctx.Step(`^a project configured for MCP mode$`, s.aProjectConfiguredForMCPMode)
	ctx.Step(`^a project configured for MCP mode with a connect timeout of (\d+) second$`, s.aProjectWithMCPModeAndConnectTimeout)
	ctx.Step(`^a workflow "([^"]*)" with one prompt step "([^"]*)"$`, s.aWorkflowWithOnePromptStep)
	ctx.Step(`^a workflow "([^"]*)" with one script step "([^"]*)" running "([^"]*)"$`, s.aWorkflowWithOneScriptStep)
	ctx.Step(`^a workflow "([^"]*)" with two sequential prompt steps "([^"]*)" and "([^"]*)"$`, s.aWorkflowWithTwoPromptSteps)

	ctx.Step(`^the MCP client connects and submits result "([^"]*)" for step "([^"]*)"$`, s.theMCPClientConnectsAndSubmitsResult)
	ctx.Step(`^the MCP client connects and handles both prompt steps$`, s.theMCPClientHandlesBothSteps)
	ctx.Step(`^the workflow "([^"]*)" executes$`, s.theWorkflowExecutes)
	ctx.Step(`^the workflow "([^"]*)" executes with no MCP client connecting$`, s.theWorkflowExecutesWithNoClient)

	ctx.Step(`^workflow run "([^"]*)" completes with result "([^"]*)"$`, s.workflowRunCompletesWith)
	ctx.Step(`^the headless claude-p executor is never invoked$`, s.theClaudePExecutorIsNeverInvoked)
	ctx.Step(`^step "([^"]*)" completes without MCP dispatch$`, s.stepCompletesWithoutMCPDispatch)
	ctx.Step(`^both steps complete using the same MCP session token$`, s.bothStepsUseSameSessionToken)
	ctx.Step(`^workflow run "([^"]*)" is marked failed$`, s.workflowRunIsMarkedFailed)
	ctx.Step(`^the failure reason mentions "([^"]*)"$`, s.theFailureReasonMentions)
}

// ─── L1 step implementations ──────────────────────────────────────────────────

func (s *mcpModeCtx) aConfigTOMLWithAgentMode(mode string) error {
	s.configContent = `[agent]
mode = "` + mode + `"
`
	return nil
}

func (s *mcpModeCtx) aConfigTOMLWithNoAgentMode() error {
	s.configContent = ""
	return nil
}

func (s *mcpModeCtx) theMCPConfigIsLoaded() error {
	return errors.New("pending: L1 config parsing implementation")
}

func (s *mcpModeCtx) theLoadedConfigHasAgentMode(mode string) error {
	return errors.New("pending: L1 config parsing implementation")
}

// ─── L2 step implementations ──────────────────────────────────────────────────

func (s *mcpModeCtx) theDaemonIsRunningInMCPModeForRun(runID string) error {
	s.runID = runID
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) theMCPClientCallsInitForRun(runID string) error {
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) theInitResponseContainsANonEmptySessionToken() error {
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) anMCPSessionHasPendingPrompt(runID, prompt string) error {
	s.runID = runID
	s.nextPrompt = prompt
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) theMCPClientCallsNext() error {
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) theNextResponseContainsPromptText(prompt string) error {
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) anMCPSessionWaitingForStep(runID, stepName string) error {
	s.runID = runID
	s.stepName = stepName
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) theMCPClientCallsSubmitResult(result, output string, tokens int) error {
	s.stepResult = result
	s.stepOutput = output
	s.outputTokens = tokens
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) stepIsMarkedCompleteWithResult(stepName, runID, result string) error {
	return errors.New("pending: L2 MCP server implementation")
}

func (s *mcpModeCtx) outputTokensRecordedForStep(tokens int, stepName, runID string) error {
	return errors.New("pending: L2 MCP server implementation")
}

// ─── L3 step implementations ──────────────────────────────────────────────────

func (s *mcpModeCtx) aProjectConfiguredForMCPMode() error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) aProjectWithMCPModeAndConnectTimeout(seconds int) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) aWorkflowWithOnePromptStep(workflowName, stepName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) aWorkflowWithOneScriptStep(workflowName, stepName, command string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) aWorkflowWithTwoPromptSteps(workflowName, step1, step2 string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theMCPClientConnectsAndSubmitsResult(result, stepName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theMCPClientHandlesBothSteps() error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theWorkflowExecutes(workflowName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theWorkflowExecutesWithNoClient(workflowName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) workflowRunCompletesWith(workflowName, result string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theClaudePExecutorIsNeverInvoked() error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) stepCompletesWithoutMCPDispatch(stepName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) bothStepsUseSameSessionToken() error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) workflowRunIsMarkedFailed(workflowName string) error {
	return errors.New("pending: L3 end-to-end implementation")
}

func (s *mcpModeCtx) theFailureReasonMentions(text string) error {
	return errors.New("pending: L3 end-to-end implementation")
}
