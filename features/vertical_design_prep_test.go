package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cucumber/godog"
)

// verticalDesignPrepCtx holds per-scenario state for vertical design-prep BDD scenarios.
type verticalDesignPrepCtx struct {
	// L1: DSL topology
	verticalDSLContent  string
	verticalDSLParseErr error
	parsedWorkflows     map[string]*domain.Workflow

	// L2: skip-check classifier
	ticketDescription string
	designDocPath     string
	classifierResult  string

	// L2: script behavior simulation
	featureName       string
	remoteHasBranches []string
	kvStore           map[string]string
	mergeOrder        []string
	testPlanBase      string
	tempDir           string
}

func (s *verticalDesignPrepCtx) reset() {
	if s.tempDir != "" {
		os.RemoveAll(s.tempDir)
	}
	*s = verticalDesignPrepCtx{
		kvStore: make(map[string]string),
	}
}

// verticalClocheDir returns the path to the .cloche/ directory inside the
// repos/cloche project (two directories up from this source file: features/ → project root).
func verticalClocheDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile: .../repos/cloche/features/vertical_design_prep_test.go
	// up 2 levels: features → repos/cloche
	root := filepath.Dir(filepath.Dir(thisFile))
	return filepath.Join(root, ".cloche")
}

// ─── L1: DSL topology steps ──────────────────────────────────────────────────

func (s *verticalDesignPrepCtx) theVerticalWorkflowDSL() error {
	path := filepath.Join(verticalClocheDir(), "vertical.cloche")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading vertical.cloche at %s: %w", path, err)
	}
	s.verticalDSLContent = string(data)
	return nil
}

func (s *verticalDesignPrepCtx) theVerticalDSLIsParsed() error {
	workflows, err := dsl.ParseAll(s.verticalDSLContent)
	s.parsedWorkflows = workflows
	s.verticalDSLParseErr = err
	return nil
}

func (s *verticalDesignPrepCtx) noVerticalDSLParseError() error {
	if s.verticalDSLParseErr != nil {
		return fmt.Errorf("unexpected DSL parse error: %w", s.verticalDSLParseErr)
	}
	return nil
}

func (s *verticalDesignPrepCtx) verticalWorkflowContainsStep(stepName string) error {
	wf, ok := s.parsedWorkflows["vertical"]
	if !ok {
		return fmt.Errorf("workflow %q not found in vertical.cloche", "main")
	}
	if _, exists := wf.Steps[stepName]; !exists {
		return fmt.Errorf("step %q not found in the main vertical workflow", stepName)
	}
	return nil
}

func (s *verticalDesignPrepCtx) verticalWorkflowWireGoesTo(fromStep, onResult, toStep string) error {
	wf, ok := s.parsedWorkflows["vertical"]
	if !ok {
		return fmt.Errorf("workflow %q not found in vertical.cloche", "main")
	}
	for _, w := range wf.Wiring {
		if w.From == fromStep && w.Result == onResult {
			if w.To != toStep {
				return fmt.Errorf("wire from %q on %q goes to %q, want %q", fromStep, onResult, w.To, toStep)
			}
			return nil
		}
	}
	return fmt.Errorf("no wire from %q on %q found in the main vertical workflow", fromStep, onResult)
}

func (s *verticalDesignPrepCtx) addressDesignFeedbackHasMaxAttempts(stepName string, maxAttempts int) error {
	wf, ok := s.parsedWorkflows["vertical"]
	if !ok {
		return fmt.Errorf("workflow %q not found in vertical.cloche", "main")
	}
	step, exists := wf.Steps[stepName]
	if !exists {
		return fmt.Errorf("step %q not found in the main vertical workflow", stepName)
	}
	got, ok := step.Config["max_attempts"]
	if !ok {
		return fmt.Errorf("step %q has no max_attempts configured", stepName)
	}
	if got != strconv.Itoa(maxAttempts) {
		return fmt.Errorf("step %q max_attempts = %s, want %d", stepName, got, maxAttempts)
	}
	return nil
}

// ─── L2: Skip-check classifier steps ─────────────────────────────────────────

func (s *verticalDesignPrepCtx) aDesignCheckTicketDescriptionReferencing(docPath string) error {
	s.designDocPath = docPath
	s.ticketDescription = "This feature ships Phase 0.5. See " + docPath + " for the design."
	return nil
}

func (s *verticalDesignPrepCtx) aDesignCheckTicketDescriptionWithNoDocsPlanReference() error {
	s.ticketDescription = "Simple feature with no design doc reference."
	s.designDocPath = ""
	return nil
}

func (s *verticalDesignPrepCtx) thatDesignDocFileExistsAndContainsApproved(keyword string) error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) thatDesignDocFileExistsButDoesNotContain(keyword string) error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) theDesignNeededClassifierRuns() error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) theClassifierResultIs(result string) error {
	return godog.ErrPending
}

// ─── L2: Script behavior steps ───────────────────────────────────────────────

func (s *verticalDesignPrepCtx) aVerticalFeatureNamed(featureName string) error {
	s.featureName = featureName
	return nil
}

func (s *verticalDesignPrepCtx) theRemoteHasABranch(branchName string) error {
	s.remoteHasBranches = append(s.remoteHasBranches, branchName)
	return nil
}

func (s *verticalDesignPrepCtx) theKVStoreHasKeySetTo(key, value string) error {
	s.kvStore[key] = value
	return nil
}

func (s *verticalDesignPrepCtx) verticalFinalizeRunsFor(featureName string) error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) theDesignBranchIsMergedBeforeTestPlan() error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) thePrepareTestPlanBranchScriptRunsFor(featureName string) error {
	return godog.ErrPending
}

func (s *verticalDesignPrepCtx) theNewTestPlanBranchIsCreatedOff(baseBranch string) error {
	return godog.ErrPending
}

// ─── Step registration ────────────────────────────────────────────────────────

func initVerticalDesignPrepScenarios(ctx *godog.ScenarioContext) {
	s := &verticalDesignPrepCtx{kvStore: make(map[string]string)}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// L1: DSL topology
	ctx.Step(`^the vertical workflow DSL$`, s.theVerticalWorkflowDSL)
	ctx.Step(`^the vertical DSL is parsed$`, s.theVerticalDSLIsParsed)
	ctx.Step(`^no vertical DSL parse error is returned$`, s.noVerticalDSLParseError)
	ctx.Step(`^the vertical workflow contains step "([^"]*)"$`, s.verticalWorkflowContainsStep)
	ctx.Step(`^in the vertical workflow the wire from "([^"]*)" on "([^"]*)" goes to "([^"]*)"$`, s.verticalWorkflowWireGoesTo)
	ctx.Step(`^step "([^"]*)" in the vertical workflow has max_attempts of (\d+)$`, s.addressDesignFeedbackHasMaxAttempts)

	// L2: Skip-check classifier
	ctx.Step(`^a design-check ticket description referencing "([^"]*)"$`, s.aDesignCheckTicketDescriptionReferencing)
	ctx.Step(`^a design-check ticket description with no docs/plans reference$`, s.aDesignCheckTicketDescriptionWithNoDocsPlanReference)
	ctx.Step(`^that design doc file exists and contains "([^"]*)"$`, s.thatDesignDocFileExistsAndContainsApproved)
	ctx.Step(`^that design doc file exists but does not contain "([^"]*)"$`, s.thatDesignDocFileExistsButDoesNotContain)
	ctx.Step(`^the design-needed classifier runs$`, s.theDesignNeededClassifierRuns)
	ctx.Step(`^the classifier result is "([^"]*)"$`, s.theClassifierResultIs)

	// L2: Script behaviors
	ctx.Step(`^a vertical feature named "([^"]*)"$`, s.aVerticalFeatureNamed)
	ctx.Step(`^the remote has a branch "([^"]*)"$`, s.theRemoteHasABranch)
	ctx.Step(`^the KV store has "([^"]*)" set to "([^"]*)"$`, s.theKVStoreHasKeySetTo)
	ctx.Step(`^vertical-finalize runs for "([^"]*)"$`, s.verticalFinalizeRunsFor)
	ctx.Step(`^the design branch is merged before the test-plan branch$`, s.theDesignBranchIsMergedBeforeTestPlan)
	ctx.Step(`^the prepare-test-plan-branch script runs for "([^"]*)"$`, s.thePrepareTestPlanBranchScriptRunsFor)
	ctx.Step(`^the new test-plan branch is created off "([^"]*)"$`, s.theNewTestPlanBranchIsCreatedOff)
}
