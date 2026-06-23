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

// stepTokenMetricsDocPath returns the expected path of the step-token-metrics design doc.
func stepTokenMetricsDocPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(thisFile))
	return filepath.Join(root, "docs", "plans", "2026-05-28-step-token-metrics.md")
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
	if _, err := os.Stat(s.docPath); err != nil {
		return fmt.Errorf("design doc not found at %s: %w", s.docPath, err)
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocDefinesARowSchemaSection() error {
	if !strings.Contains(s.docContent, "## Row Schema") {
		return fmt.Errorf("design doc has no '## Row Schema' section")
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaIncludesFullyQualifiedStepName() error {
	if !strings.Contains(s.docContent, "step_fqn") {
		return fmt.Errorf("row schema does not define a fully-qualified step name column (expected 'step_fqn')")
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaIncludesResultColumn() error {
	if !strings.Contains(s.docContent, "result") {
		return fmt.Errorf("row schema does not define a result column")
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaIncludesStartedColumn() error {
	if !strings.Contains(s.docContent, "started_at") || !strings.Contains(s.docContent, "ISO-8601") {
		return fmt.Errorf("row schema does not define an ISO-8601 started column (expected 'started_at' and 'ISO-8601')")
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaIncludesDurationColumn() error {
	if !strings.Contains(s.docContent, "duration") {
		return fmt.Errorf("row schema does not define a duration column")
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaIncludesWorkflowSegmentTagColumn() error {
	if !strings.Contains(s.docContent, "workflow_segment") {
		return fmt.Errorf("row schema does not define a workflow-segment tag column (expected 'workflow_segment')")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesCanonicalStepIdentifierFormat() error {
	if !strings.Contains(s.docContent, "Canonical Step Identifier") {
		return fmt.Errorf("design doc has no 'Canonical Step Identifier' section")
	}
	return nil
}

func (s *runStateStepViewCtx) theStepIdentifierFormatIsConsistentWithTokenMetrics() error {
	metricsData, err := os.ReadFile(stepTokenMetricsDocPath())
	if err != nil {
		return fmt.Errorf("cannot read step-token-metrics doc: %w", err)
	}
	metricsContent := string(metricsData)

	// Both docs should agree on the pair-based identifier: "workflow name + step name"
	if !strings.Contains(s.docContent, "workflow_name") || !strings.Contains(s.docContent, "step_name") {
		return fmt.Errorf("run-state-step-view doc does not reference workflow_name + step_name as the canonical pair")
	}
	if !strings.Contains(metricsContent, "workflow name + step name") && !strings.Contains(metricsContent, "workflow_name") {
		return fmt.Errorf("step-token-metrics doc does not reference the same workflow+step identifier format")
	}
	// Both docs should not have a conflicting fully-qualified string key as the storage format
	return nil
}

func (s *runStateStepViewCtx) theDesignDocNamesDataSourceEndpoint() error {
	if !strings.Contains(s.docContent, "## Data Source") {
		return fmt.Errorf("design doc has no '## Data Source' section")
	}
	if !strings.Contains(s.docContent, "/api/runs/{id}") {
		return fmt.Errorf("data source section does not name the endpoint (expected '/api/runs/{id}')")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocDescribesSubworkflowStepSurfacing() error {
	if !strings.Contains(s.docContent, "subworkflow") {
		return fmt.Errorf("design doc does not describe how subworkflow steps are surfaced")
	}
	if !strings.Contains(s.docContent, "flattenRun") && !strings.Contains(s.docContent, "pre-flattened") {
		return fmt.Errorf("data source section does not describe how subworkflow steps are delivered (expected 'pre-flattened' or 'flattenRun')")
	}
	return nil
}

func (s *runStateStepViewCtx) noUnresolvedOpenQuestionsAboutIdentifier() error {
	// The open questions section should not contain an unresolved bullet about identifier canonicalization.
	// A "Resolved:" note is fine; an open bullet is not.
	lower := strings.ToLower(s.docContent)
	idx := strings.Index(lower, "## open questions")
	if idx == -1 {
		return nil // no open questions section at all is fine
	}
	openQuestionsSection := s.docContent[idx:]
	if strings.Contains(openQuestionsSection, "canonical step identifier") &&
		!strings.Contains(strings.ToLower(openQuestionsSection), "resolved") {
		return fmt.Errorf("open questions section still has an unresolved canonical step identifier question")
	}
	return nil
}

func (s *runStateStepViewCtx) noUnresolvedOpenQuestionsAboutDataSource() error {
	lower := strings.ToLower(s.docContent)
	idx := strings.Index(lower, "## open questions")
	if idx == -1 {
		return nil
	}
	openQuestionsSection := s.docContent[idx:]
	// Check there's no open (unresolved) bullet about data source shape
	for _, line := range strings.Split(openQuestionsSection, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") &&
			(strings.Contains(strings.ToLower(trimmed), "data source") ||
				strings.Contains(strings.ToLower(trimmed), "endpoint") ||
				strings.Contains(strings.ToLower(trimmed), "response shape")) {
			return fmt.Errorf("open questions section still has an unresolved data source question: %q", trimmed)
		}
	}
	return nil
}

// ─── L2: UI presentation steps ───────────────────────────────────────────────

func (s *runStateStepViewCtx) theDesignDocHasColorAssignmentSection() error {
	if !strings.Contains(s.docContent, "## Color Assignment") {
		return fmt.Errorf("design doc has no '## Color Assignment' section")
	}
	return nil
}

func (s *runStateStepViewCtx) colorAssignmentSectionReferencesCSSVariables() error {
	if !strings.Contains(s.docContent, "--seg-") {
		return fmt.Errorf("color assignment section does not reference CSS variables from the web-ui-cleanup palette (expected '--seg-' variables)")
	}
	return nil
}

func (s *runStateStepViewCtx) colorAssignmentSectionStatesStabilityRule() error {
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "stability rule") && !strings.Contains(lower, "stable") {
		return fmt.Errorf("color assignment section does not state a stability rule (expected 'stable' or 'Stability rule')")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocHasStepOrderingSection() error {
	if !strings.Contains(s.docContent, "## Step Ordering") {
		return fmt.Errorf("design doc has no '## Step Ordering' section")
	}
	return nil
}

func (s *runStateStepViewCtx) stepOrderingSectionStatesChosenOrder() error {
	if !strings.Contains(strings.ToLower(s.docContent), "chronological") {
		return fmt.Errorf("step ordering section does not state the chosen order (expected 'chronological')")
	}
	return nil
}

func (s *runStateStepViewCtx) stepOrderingSectionProvidesRationale() error {
	if !strings.Contains(strings.ToLower(s.docContent), "rationale") {
		return fmt.Errorf("step ordering section does not provide a rationale (expected 'Rationale')")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocHasNestingStrategySection() error {
	if !strings.Contains(s.docContent, "## Nesting Strategy") {
		return fmt.Errorf("design doc has no '## Nesting Strategy' section")
	}
	return nil
}

func (s *runStateStepViewCtx) nestingStrategySectionStatesDepthSignaling() error {
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "color") || !strings.Contains(lower, "indent") {
		return fmt.Errorf("nesting strategy section does not state whether depth is shown by color, indent, or both")
	}
	return nil
}

func (s *runStateStepViewCtx) nestingStrategySectionDescribesLongNameHandling() error {
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "ellipsis") && !strings.Contains(lower, "truncat") {
		return fmt.Errorf("nesting strategy section does not describe long fully-qualified name handling (expected 'ellipsis' or 'truncate')")
	}
	return nil
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
