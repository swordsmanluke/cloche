package features_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cucumber/godog"
)

// runStateStepViewCtx holds per-scenario state for the run-state per-step view design doc BDD scenarios.
type runStateStepViewCtx struct {
	docPath    string
	docContent string
	docLoaded  bool
}

func (s *runStateStepViewCtx) reset() {
	*s = runStateStepViewCtx{}
}

// designDocPath returns the expected path of the design doc relative to the repo root.
func runStateDesignDocPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(thisFile))
	return filepath.Join(root, "docs", "plans", "2026-05-28-run-state-step-view.md")
}

func (s *runStateStepViewCtx) theRunStateStepViewDesignDoc() error {
	s.docPath = runStateDesignDocPath()
	data, err := os.ReadFile(s.docPath)
	if err != nil {
		// We still set the path; the "file exists" step will surface the error.
		s.docContent = ""
		s.docLoaded = false
		return nil
	}
	s.docContent = string(data)
	s.docLoaded = true
	return nil
}

// ─── L1: Data model steps ────────────────────────────────────────────────────

func (s *runStateStepViewCtx) theDesignDocFileExists() error {
	return errors.New("pending: L1 implementation — docs/plans/2026-05-28-run-state-step-view.md does not exist yet")
}

func (s *runStateStepViewCtx) theDesignDocDefinesARowSchemaSection() error {
	return errors.New("pending: L1 implementation — row schema section not yet written")
}

func (s *runStateStepViewCtx) theRowSchemaIncludesFullyQualifiedStepName() error {
	return errors.New("pending: L1 implementation — fully-qualified step name column not yet specified")
}

func (s *runStateStepViewCtx) theRowSchemaIncludesResultColumn() error {
	return errors.New("pending: L1 implementation — result column not yet specified")
}

func (s *runStateStepViewCtx) theRowSchemaIncludesStartedColumn() error {
	return errors.New("pending: L1 implementation — ISO-8601 started column not yet specified")
}

func (s *runStateStepViewCtx) theRowSchemaIncludesDurationColumn() error {
	return errors.New("pending: L1 implementation — duration column not yet specified")
}

func (s *runStateStepViewCtx) theRowSchemaIncludesWorkflowSegmentTagColumn() error {
	return errors.New("pending: L1 implementation — workflow-segment tag column not yet specified")
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesCanonicalStepIdentifierFormat() error {
	return errors.New("pending: L1 implementation — canonical step identifier format not yet decided")
}

func (s *runStateStepViewCtx) theStepIdentifierFormatIsConsistentWithTokenMetrics() error {
	return errors.New("pending: L1 implementation — step identifier consistency with step-token-metrics doc not yet verified")
}

func (s *runStateStepViewCtx) theDesignDocNamesDataSourceEndpoint() error {
	return errors.New("pending: L1 implementation — data source endpoint not yet named")
}

func (s *runStateStepViewCtx) theDesignDocDescribesSubworkflowStepSurfacing() error {
	return errors.New("pending: L1 implementation — subworkflow step surfacing in response not yet described")
}

// ─── L2: UI presentation steps ───────────────────────────────────────────────

func (s *runStateStepViewCtx) theDesignDocHasColorAssignmentSection() error {
	return errors.New("pending: L2 implementation — color assignment section not yet written")
}

func (s *runStateStepViewCtx) colorAssignmentSectionReferencesCSSVariables() error {
	return errors.New("pending: L2 implementation — CSS variable references not yet written")
}

func (s *runStateStepViewCtx) colorAssignmentSectionStatesStabilityRule() error {
	return errors.New("pending: L2 implementation — color stability rule not yet specified")
}

func (s *runStateStepViewCtx) theDesignDocHasStepOrderingSection() error {
	return errors.New("pending: L2 implementation — step ordering section not yet written")
}

func (s *runStateStepViewCtx) stepOrderingSectionStatesChosenOrder() error {
	return errors.New("pending: L2 implementation — step ordering strategy not yet decided")
}

func (s *runStateStepViewCtx) stepOrderingSectionProvidesRationale() error {
	return errors.New("pending: L2 implementation — step ordering rationale not yet written")
}

func (s *runStateStepViewCtx) theDesignDocHasNestingStrategySection() error {
	return errors.New("pending: L2 implementation — nesting strategy section not yet written")
}

func (s *runStateStepViewCtx) nestingStrategySectionStatesDepthSignaling() error {
	return errors.New("pending: L2 implementation — depth signaling strategy (color vs indent) not yet decided")
}

func (s *runStateStepViewCtx) nestingStrategySectionDescribesLongNameHandling() error {
	return errors.New("pending: L2 implementation — long fully-qualified name handling not yet specified")
}

func (s *runStateStepViewCtx) noUnresolvedOpenQuestionsAboutIdentifier() error {
	return errors.New("pending: L1 implementation — identifier canonicalization open question not yet resolved")
}

func (s *runStateStepViewCtx) noUnresolvedOpenQuestionsAboutDataSource() error {
	return errors.New("pending: L1 implementation — data source shape open question not yet resolved")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initRunStateStepViewScenarios(ctx *godog.ScenarioContext) {
	s := &runStateStepViewCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	ctx.Step(`^the run-state step-view design doc$`, s.theRunStateStepViewDesignDoc)

	// L1: Data model
	ctx.Step(`^the design doc file exists$`, s.theDesignDocFileExists)
	ctx.Step(`^the design doc defines a row schema section$`, s.theDesignDocDefinesARowSchemaSection)
	ctx.Step(`^the row schema includes a fully-qualified step name column$`, s.theRowSchemaIncludesFullyQualifiedStepName)
	ctx.Step(`^the row schema includes a result column$`, s.theRowSchemaIncludesResultColumn)
	ctx.Step(`^the row schema includes an ISO-8601 started column$`, s.theRowSchemaIncludesStartedColumn)
	ctx.Step(`^the row schema includes a duration column$`, s.theRowSchemaIncludesDurationColumn)
	ctx.Step(`^the row schema includes a workflow-segment tag column for color grouping$`, s.theRowSchemaIncludesWorkflowSegmentTagColumn)
	ctx.Step(`^the design doc specifies the canonical step identifier format$`, s.theDesignDocSpecifiesCanonicalStepIdentifierFormat)
	ctx.Step(`^the step identifier format is consistent with the step-token-metrics design doc$`, s.theStepIdentifierFormatIsConsistentWithTokenMetrics)
	ctx.Step(`^the design doc names a data source endpoint$`, s.theDesignDocNamesDataSourceEndpoint)
	ctx.Step(`^the design doc describes how subworkflow steps are surfaced in the response$`, s.theDesignDocDescribesSubworkflowStepSurfacingInTheResponse)

	// L2: UI presentation
	ctx.Step(`^the design doc has a color assignment section$`, s.theDesignDocHasColorAssignmentSection)
	ctx.Step(`^the color assignment section references CSS variables from the web-ui-cleanup palette$`, s.colorAssignmentSectionReferencesCSSVariables)
	ctx.Step(`^the color assignment section states a stability rule$`, s.colorAssignmentSectionStatesStabilityRule)
	ctx.Step(`^the design doc has a step ordering section$`, s.theDesignDocHasStepOrderingSection)
	ctx.Step(`^the step ordering section states the chosen order$`, s.stepOrderingSectionStatesChosenOrder)
	ctx.Step(`^the step ordering section provides a rationale for the choice$`, s.stepOrderingSectionProvidesRationale)
	ctx.Step(`^the design doc has a nesting strategy section$`, s.theDesignDocHasNestingStrategySection)
	ctx.Step(`^the nesting strategy section states whether depth is shown by color, indent, or both$`, s.nestingStrategySectionStatesDepthSignaling)
	ctx.Step(`^the nesting strategy section describes long fully-qualified name handling$`, s.nestingStrategySectionDescribesLongNameHandling)
	ctx.Step(`^the design doc has no unresolved open questions about identifier canonicalization$`, s.noUnresolvedOpenQuestionsAboutIdentifier)
	ctx.Step(`^the design doc has no unresolved open questions about the data source shape$`, s.noUnresolvedOpenQuestionsAboutDataSource)
}

// theDesignDocDescribesSubworkflowStepSurfacingInTheResponse is the step definition
// for the long step text that maps to theDesignDocDescribesSubworkflowStepSurfacing.
func (s *runStateStepViewCtx) theDesignDocDescribesSubworkflowStepSurfacingInTheResponse() error {
	return s.theDesignDocDescribesSubworkflowStepSurfacing()
}
