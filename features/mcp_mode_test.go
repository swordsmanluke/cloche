package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

type mcpModeCtx struct{}

func (s *mcpModeCtx) reset() {
	*s = mcpModeCtx{}
}

func initMCPModeScenarios(ctx *godog.ScenarioContext) {
	s := &mcpModeCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Mode selection
	ctx.Step(`^a workflow file with mode set to "([^"]*)"$`, s.aWorkflowFileWithMode)
	ctx.Step(`^the MCP workflow file is parsed$`, s.theMCPWorkflowFileIsParsed)
	ctx.Step(`^no MCP DSL error is returned$`, s.noMCPDSLErrorIsReturned)
	ctx.Step(`^the parsed workflow has mode "([^"]*)"$`, s.theParsedWorkflowHasMode)
	ctx.Step(`^a running daemon with a test project for MCP$`, s.aRunningDaemonForMCP)
	ctx.Step(`^the user starts "([^"]*)"$`, s.theUserStartsCommand)
	ctx.Step(`^the MCP-mode run is created successfully$`, s.theMCPModeRunIsCreated)

	// MCP server startup and handshake
	ctx.Step(`^an MCP-mode run is in progress at the daemon address$`, s.anMCPModeRunIsInProgress)
	ctx.Step(`^the interactive Claude agent calls the MCP init tool$`, s.agentCallsMCPInit)
	ctx.Step(`^the agent receives a valid MCP session token$`, s.agentReceivesSessionToken)

	// Control-flow: poll and submit-result
	ctx.Step(`^an MCP session is active with a prompt step pending$`, s.mcpSessionWithPromptStepPending)
	ctx.Step(`^the agent calls the MCP poll tool$`, s.agentCallsMCPPoll)
	ctx.Step(`^the agent receives the prompt text for the pending step$`, s.agentReceivesPromptText)
	ctx.Step(`^an MCP session has a prompt step in-flight with wires "([^"]*)" and "([^"]*)"$`, s.mcpSessionWithWiredStep)
	ctx.Step(`^the agent calls the MCP submit-result tool with named result "([^"]*)"$`, s.agentSubmitsNamedResult)
	ctx.Step(`^the run advances to the step wired to "([^"]*)"$`, s.runAdvancesToWiredStep)

	// Script steps unaffected
	ctx.Step(`^an MCP-mode run reaches a script step$`, s.mcpRunReachesScriptStep)
	ctx.Step(`^the script step executes$`, s.scriptStepExecutes)
	ctx.Step(`^the script step completes without requiring a submit-result call$`, s.scriptStepCompletesWithoutMCP)

	// Conversation continuity
	ctx.Step(`^an MCP session is active with two sequential prompt steps$`, s.mcpSessionWithTwoSteps)
	ctx.Step(`^the agent has submitted the first prompt step with result "([^"]*)"$`, s.firstStepSubmitted)
	ctx.Step(`^the agent calls the MCP poll tool for the next step$`, s.agentPollsForNextStep)
	ctx.Step(`^the agent receives the second prompt including prior conversation context$`, s.agentReceivesSecondStepWithContext)

	// Result protocol
	ctx.Step(`^an MCP session has a prompt step pending$`, s.mcpSessionHasPromptStepPending)
	ctx.Step(`^the run follows the "([^"]*)" wire$`, s.runFollowsWire)
	ctx.Step(`^no CLOCHE_RESULT marker appears in the captured output$`, s.noCLOCHE_RESULTMarker)
}

// ─── Mode selection stubs ─────────────────────────────────────────────────────

func (s *mcpModeCtx) aWorkflowFileWithMode(_ string) error {
	return errors.New("pending: MCP DSL mode field implementation")
}

func (s *mcpModeCtx) theMCPWorkflowFileIsParsed() error {
	return errors.New("pending: MCP DSL mode field implementation")
}

func (s *mcpModeCtx) noMCPDSLErrorIsReturned() error {
	return errors.New("pending: MCP DSL mode field implementation")
}

func (s *mcpModeCtx) theParsedWorkflowHasMode(_ string) error {
	return errors.New("pending: MCP DSL mode field implementation")
}

func (s *mcpModeCtx) aRunningDaemonForMCP() error {
	return errors.New("pending: MCP mode run creation implementation")
}

func (s *mcpModeCtx) theUserStartsCommand(_ string) error {
	return errors.New("pending: MCP mode run creation implementation")
}

func (s *mcpModeCtx) theMCPModeRunIsCreated() error {
	return errors.New("pending: MCP mode run creation implementation")
}

// ─── MCP server startup and handshake stubs ───────────────────────────────────

func (s *mcpModeCtx) anMCPModeRunIsInProgress() error {
	return errors.New("pending: MCP server implementation")
}

func (s *mcpModeCtx) agentCallsMCPInit() error {
	return errors.New("pending: MCP server implementation")
}

func (s *mcpModeCtx) agentReceivesSessionToken() error {
	return errors.New("pending: MCP server implementation")
}

// ─── Control-flow stubs ───────────────────────────────────────────────────────

func (s *mcpModeCtx) mcpSessionWithPromptStepPending() error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

func (s *mcpModeCtx) agentCallsMCPPoll() error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

func (s *mcpModeCtx) agentReceivesPromptText() error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

func (s *mcpModeCtx) mcpSessionWithWiredStep(_, _ string) error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

func (s *mcpModeCtx) agentSubmitsNamedResult(_ string) error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

func (s *mcpModeCtx) runAdvancesToWiredStep(_ string) error {
	return errors.New("pending: MCP poll/submit-result implementation")
}

// ─── Script steps unaffected stubs ───────────────────────────────────────────

func (s *mcpModeCtx) mcpRunReachesScriptStep() error {
	return errors.New("pending: MCP mode script step pass-through implementation")
}

func (s *mcpModeCtx) scriptStepExecutes() error {
	return errors.New("pending: MCP mode script step pass-through implementation")
}

func (s *mcpModeCtx) scriptStepCompletesWithoutMCP() error {
	return errors.New("pending: MCP mode script step pass-through implementation")
}

// ─── Conversation continuity stubs ───────────────────────────────────────────

func (s *mcpModeCtx) mcpSessionWithTwoSteps() error {
	return errors.New("pending: MCP conversation continuity implementation")
}

func (s *mcpModeCtx) firstStepSubmitted(_ string) error {
	return errors.New("pending: MCP conversation continuity implementation")
}

func (s *mcpModeCtx) agentPollsForNextStep() error {
	return errors.New("pending: MCP conversation continuity implementation")
}

func (s *mcpModeCtx) agentReceivesSecondStepWithContext() error {
	return errors.New("pending: MCP conversation continuity implementation")
}

// ─── Result protocol stubs ────────────────────────────────────────────────────

func (s *mcpModeCtx) mcpSessionHasPromptStepPending() error {
	return errors.New("pending: MCP result protocol implementation")
}

func (s *mcpModeCtx) runFollowsWire(_ string) error {
	return errors.New("pending: MCP result protocol implementation")
}

func (s *mcpModeCtx) noCLOCHE_RESULTMarker() error {
	return errors.New("pending: MCP result protocol implementation")
}
