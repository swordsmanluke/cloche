package features_test

import (
	"context"
	"errors"
	"strings"

	"github.com/cucumber/godog"
)

// designPrepCtx holds per-scenario state for vertical design-prep BDD scenarios.
type designPrepCtx struct {
	// L1: DSL parsing
	dslContent     string
	dslValidateErr error

	// L2: Runtime simulation
	ticketDescription string
	designDocPath     string
	designDocStatus   string
	designDocContent  string
	checkResult       string
	prBody            string
	mergeOrder        []string
}

func (s *designPrepCtx) reset() {
	*s = designPrepCtx{}
}

// ─── L1: DSL parsing steps ───────────────────────────────────────────────────

func (s *designPrepCtx) aVerticalDSLFileContainingPhase05Step() error {
	return errors.New("pending: L1 workflow wiring implementation")
}

func (s *designPrepCtx) theDesignPrepDSLFileIsValidated() error {
	return errors.New("pending: L1 workflow wiring implementation")
}

func (s *designPrepCtx) noDesignPrepValidationErrorIsReturned() error {
	return errors.New("pending: L1 workflow wiring implementation")
}

func (s *designPrepCtx) inDesignPrepWorkflowStepRoutesTo(fromStep, result, toStep string) error {
	return errors.New("pending: L1 workflow wiring implementation")
}

func (s *designPrepCtx) inDesignPrepWorkflowStepHasMaxAttempts(stepName string, maxAttempts int) error {
	return errors.New("pending: L1 workflow wiring implementation")
}

// ─── L2: Runtime skip-check and script behavior steps ────────────────────────

func (s *designPrepCtx) aTicketDescriptionReferencesDesignDoc(docPath string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) thatDesignDocExistsWithStatus(status string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) aTicketDescriptionWithNoDocsPlansReference() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) checkDesignNeededEvaluatesTheTicket() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) theCheckDesignNeededResultIs(result string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) aDesignDocContaining(content *godog.DocString) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) theDesignPRIsOpened() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) thePRBodyContains(text string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) aVerticalStackWhereDesignBranchExistsOnRemote() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) finalizeRuns() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) theMergeOrderBeginsWithDesignBranch() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *designPrepCtx) theDesignBranchIsMergedBeforeTestPlanBranch() error {
	return errors.New("pending: L2 runtime implementation")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initVerticalDesignPrepScenarios(ctx *godog.ScenarioContext) {
	s := &designPrepCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// L1: DSL parsing
	ctx.Step(`^a vertical DSL file containing the Phase 0\.5 check-design-needed step$`, s.aVerticalDSLFileContainingPhase05Step)
	ctx.Step(`^the design-prep DSL file is validated$`, s.theDesignPrepDSLFileIsValidated)
	ctx.Step(`^no design-prep validation error is returned$`, s.noDesignPrepValidationErrorIsReturned)
	ctx.Step(`^in the design-prep workflow "([^"]*)" on "([^"]*)" routes to "([^"]*)"$`, s.inDesignPrepWorkflowStepRoutesTo)
	ctx.Step(`^in the design-prep workflow step "([^"]*)" has max_attempts of (\d+)$`, s.inDesignPrepWorkflowStepHasMaxAttempts)

	// L2: Runtime skip-check
	ctx.Step(`^a ticket description that references "([^"]*)"$`, s.aTicketDescriptionReferencesDesignDoc)
	ctx.Step(`^that design doc exists with status "([^"]*)"$`, s.thatDesignDocExistsWithStatus)
	ctx.Step(`^a ticket description with no docs/plans reference$`, s.aTicketDescriptionWithNoDocsPlansReference)
	ctx.Step(`^check-design-needed evaluates the ticket$`, s.checkDesignNeededEvaluatesTheTicket)
	ctx.Step(`^the check-design-needed result is "([^"]*)"$`, s.theCheckDesignNeededResultIs)

	// L2: PR body
	ctx.Step(`^a design doc containing:$`, s.aDesignDocContaining)
	ctx.Step(`^the design PR is opened$`, s.theDesignPRIsOpened)
	ctx.Step(`^the PR body contains "([^"]*)"$`, s.thePRBodyContains)

	// L2: Finalize merge order
	ctx.Step(`^a vertical stack where the design branch exists on the remote$`, s.aVerticalStackWhereDesignBranchExistsOnRemote)
	ctx.Step(`^finalize runs$`, s.finalizeRuns)
	ctx.Step(`^the merge order begins with the design branch$`, s.theMergeOrderBeginsWithDesignBranch)
	ctx.Step(`^the design branch is merged before the test-plan branch$`, s.theDesignBranchIsMergedBeforeTestPlanBranch)
}

// suppress unused import warning during the pending phase
var _ = strings.Contains
