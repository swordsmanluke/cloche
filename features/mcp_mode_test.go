package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cucumber/godog"
)

const mcpModeDocRelPath = "docs/plans/2026-05-28-mcp-mode.md"

// mcpModeCtx holds per-scenario state for MCP mode design doc BDD scenarios.
type mcpModeCtx struct {
	docContent string
	readErr    error
}

func (s *mcpModeCtx) reset() {
	*s = mcpModeCtx{}
}

func mcpModeRepoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(thisFile))
}

// ─── Given / When ────────────────────────────────────────────────────────────

func (s *mcpModeCtx) theMCPModeDesignDoc() error {
	return nil // path is fixed; just signals intent
}

func (s *mcpModeCtx) theDesignDocIsRead() error {
	path := filepath.Join(mcpModeRepoRoot(), mcpModeDocRelPath)
	data, err := os.ReadFile(path)
	s.readErr = err
	if err == nil {
		s.docContent = string(data)
	}
	return nil
}

// ─── Then ─────────────────────────────────────────────────────────────────────

func (s *mcpModeCtx) noReadErrorIsReturned() error {
	if s.readErr != nil {
		return fmt.Errorf("could not read %s: %w", mcpModeDocRelPath, s.readErr)
	}
	return nil
}

func (s *mcpModeCtx) docContainsServerHostingDecision() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	// The doc must address where the MCP server runs: daemon / cloched or in-container
	hasDaemon := strings.Contains(low, "daemon") || strings.Contains(low, "cloched")
	hasServerSection := strings.Contains(low, "mcp server") || strings.Contains(low, "server hosting") || strings.Contains(low, "where the mcp")
	if !hasDaemon || !hasServerSection {
		return fmt.Errorf("%s does not contain a server hosting decision (need MCP server location + 'daemon'/'cloched' mention)", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docDefinesMCPTool(toolName string) error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	// accept "submit-result" or "submit_result"
	needle := strings.ToLower(strings.ReplaceAll(toolName, "-", "_"))
	alt := strings.ToLower(toolName)
	if !strings.Contains(low, needle) && !strings.Contains(low, alt) {
		return fmt.Errorf("%s does not define MCP tool %q", mcpModeDocRelPath, toolName)
	}
	return nil
}

func (s *mcpModeCtx) docDefinesResultProtocol() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	// The doc must reference the CLOCHE_RESULT marker it is replacing
	if !strings.Contains(low, "cloche_result") {
		return fmt.Errorf("%s does not reference CLOCHE_RESULT (the stdout marker being replaced)", mcpModeDocRelPath)
	}
	// And must propose an alternative result mechanism
	if !strings.Contains(low, "result protocol") && !strings.Contains(low, "submit") {
		return fmt.Errorf("%s does not define a result protocol to replace CLOCHE_RESULT", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docAddressesConcurrencyModel() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	if !strings.Contains(low, "concurren") {
		return fmt.Errorf("%s does not address the concurrency model", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docAddressesConversationContinuity() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	if !strings.Contains(low, "continuity") && !strings.Contains(low, "conversation continuity") {
		return fmt.Errorf("%s does not address conversation continuity", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docDefinesModeSelection() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	hasOptIn := strings.Contains(low, "opt in") || strings.Contains(low, "opt-in") ||
		strings.Contains(low, "mode selection") || strings.Contains(low, "mcp_mode") ||
		strings.Contains(low, "mcp mode") && strings.Contains(low, "config")
	if !hasOptIn {
		return fmt.Errorf("%s does not define how a user opts in to MCP mode", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docSpecifiesTokenAndLogFlow() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	hasTokens := strings.Contains(low, "token")
	hasLogs := strings.Contains(low, "log") || strings.Contains(low, "stream")
	if !hasTokens || !hasLogs {
		return fmt.Errorf("%s does not specify token and log flow (need both 'token' and 'log'/'stream')", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docCoversHumanInTheLoop() error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	if !strings.Contains(low, "human") {
		return fmt.Errorf("%s does not cover human-in-the-loop integration", mcpModeDocRelPath)
	}
	return nil
}

func (s *mcpModeCtx) docStatusIsNotCaptured(forbiddenStatus string) error {
	if err := s.requireDoc(); err != nil {
		return err
	}
	low := strings.ToLower(s.docContent)
	if strings.Contains(low, strings.ToLower(forbiddenStatus)) {
		return fmt.Errorf("%s still has status %q — must be promoted to Design or RFC", mcpModeDocRelPath, forbiddenStatus)
	}
	return nil
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func (s *mcpModeCtx) requireDoc() error {
	if s.readErr != nil {
		return fmt.Errorf("design doc not readable (%s): %w", mcpModeDocRelPath, s.readErr)
	}
	if s.docContent == "" {
		return fmt.Errorf("design doc not loaded — did you call 'the design doc is read'?")
	}
	return nil
}

// ─── Step registration ────────────────────────────────────────────────────────

func initMCPModeScenarios(ctx *godog.ScenarioContext) {
	s := &mcpModeCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Given / When
	ctx.Step(`^the MCP mode design doc$`, s.theMCPModeDesignDoc)
	ctx.Step(`^the design doc is read$`, s.theDesignDocIsRead)

	// Then — L1
	ctx.Step(`^no read error is returned$`, s.noReadErrorIsReturned)
	ctx.Step(`^the design doc contains a server hosting decision$`, s.docContainsServerHostingDecision)
	ctx.Step(`^the design doc defines the "([^"]*)" MCP tool$`, s.docDefinesMCPTool)
	ctx.Step(`^the design doc defines the result protocol replacing CLOCHE_RESULT$`, s.docDefinesResultProtocol)

	// Then — L2
	ctx.Step(`^the design doc addresses the concurrency model$`, s.docAddressesConcurrencyModel)
	ctx.Step(`^the design doc addresses conversation continuity$`, s.docAddressesConversationContinuity)
	ctx.Step(`^the design doc defines how a user opts in to MCP mode$`, s.docDefinesModeSelection)
	ctx.Step(`^the design doc specifies token and log flow back to the engine$`, s.docSpecifiesTokenAndLogFlow)
	ctx.Step(`^the design doc covers human-in-the-loop integration$`, s.docCoversHumanInTheLoop)
	ctx.Step(`^the design doc status is not "([^"]*)"$`, s.docStatusIsNotCaptured)
}
