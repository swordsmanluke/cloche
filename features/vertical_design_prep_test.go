package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

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

// thatDesignDocFileExistsAndContainsApproved creates the design doc in a temp directory
// with the given keyword (e.g. "**Status:** Approved") in its body.
func (s *verticalDesignPrepCtx) thatDesignDocFileExistsAndContainsApproved(keyword string) error {
	if err := s.ensureTempDir(); err != nil {
		return err
	}
	if s.designDocPath == "" {
		return fmt.Errorf("designDocPath not set — call aDesignCheckTicketDescriptionReferencing first")
	}
	fullPath := filepath.Join(s.tempDir, s.designDocPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte("# Design\n\n"+keyword+"\n"), 0o644)
}

// thatDesignDocFileExistsButDoesNotContain creates the design doc without the keyword
// (status remains Draft, so the classifier should return needs-design).
func (s *verticalDesignPrepCtx) thatDesignDocFileExistsButDoesNotContain(_ string) error {
	if err := s.ensureTempDir(); err != nil {
		return err
	}
	if s.designDocPath == "" {
		return fmt.Errorf("designDocPath not set — call aDesignCheckTicketDescriptionReferencing first")
	}
	fullPath := filepath.Join(s.tempDir, s.designDocPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte("# Design\n\n**Status:** Draft\n"), 0o644)
}

// theDesignNeededClassifierRuns implements the classification logic that mirrors what
// vertical-check-design-needed.md instructs the agent to do: find docs/plans/*.md
// references in the description, check each for existence and **Status:** Approved.
func (s *verticalDesignPrepCtx) theDesignNeededClassifierRuns() error {
	re := regexp.MustCompile(`docs/plans/[A-Za-z0-9._-]+\.md`)
	refs := re.FindAllString(s.ticketDescription, -1)

	root := s.tempDir
	if root == "" {
		root = "."
	}

	for _, ref := range refs {
		data, err := os.ReadFile(filepath.Join(root, ref))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "**Status:** Approved") {
			s.classifierResult = "has-design"
			return nil
		}
	}
	s.classifierResult = "needs-design"
	return nil
}

func (s *verticalDesignPrepCtx) theClassifierResultIs(result string) error {
	if s.classifierResult != result {
		return fmt.Errorf("classifier returned %q, want %q", s.classifierResult, result)
	}
	return nil
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

// verticalFinalizeRunsFor simulates the stack-walk ordering in vertical-finalize.sh:
// design branch (if present on remote) is added to the merge order before test-plan.
func (s *verticalDesignPrepCtx) verticalFinalizeRunsFor(featureName string) error {
	s.mergeOrder = nil
	designBranch := "vertical/" + featureName + "/design"
	testPlanBranch := "vertical/" + featureName + "/test-plan"

	for _, b := range s.remoteHasBranches {
		if b == designBranch {
			s.mergeOrder = append(s.mergeOrder, b)
			break
		}
	}
	for _, b := range s.remoteHasBranches {
		if b == testPlanBranch {
			s.mergeOrder = append(s.mergeOrder, b)
			break
		}
	}
	return nil
}

func (s *verticalDesignPrepCtx) theDesignBranchIsMergedBeforeTestPlan() error {
	designIdx, testPlanIdx := -1, -1
	for i, b := range s.mergeOrder {
		if strings.HasSuffix(b, "/design") {
			designIdx = i
		}
		if strings.HasSuffix(b, "/test-plan") {
			testPlanIdx = i
		}
	}
	if designIdx == -1 {
		return fmt.Errorf("design branch not in merge order %v", s.mergeOrder)
	}
	if testPlanIdx == -1 {
		return fmt.Errorf("test-plan branch not in merge order %v", s.mergeOrder)
	}
	if designIdx >= testPlanIdx {
		return fmt.Errorf("design (idx %d) not before test-plan (idx %d) in %v", designIdx, testPlanIdx, s.mergeOrder)
	}
	return nil
}

// thePrepareTestPlanBranchScriptRunsFor simulates vertical-prepare-test-plan-branch.sh:
// uses design_branch from KV if set; falls back to vertical_base_branch or "main".
func (s *verticalDesignPrepCtx) thePrepareTestPlanBranchScriptRunsFor(_ string) error {
	if db := s.kvStore["design_branch"]; db != "" {
		s.testPlanBase = db
	} else {
		base := s.kvStore["vertical_base_branch"]
		if base == "" {
			base = "main"
		}
		s.testPlanBase = base
	}
	return nil
}

func (s *verticalDesignPrepCtx) theNewTestPlanBranchIsCreatedOff(baseBranch string) error {
	if s.testPlanBase != baseBranch {
		return fmt.Errorf("test-plan branch based off %q, want %q", s.testPlanBase, baseBranch)
	}
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (s *verticalDesignPrepCtx) ensureTempDir() error {
	if s.tempDir != "" {
		return nil
	}
	var err error
	s.tempDir, err = os.MkdirTemp("", "design-check-*")
	return err
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
