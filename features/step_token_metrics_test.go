package features_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cucumber/godog"
)

type stepTokenMetricsCtx struct {
	docPath    string
	docContent string
	docLoaded  bool
}

func (s *stepTokenMetricsCtx) reset() {
	*s = stepTokenMetricsCtx{}
}

func tokenMetricsDocPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(thisFile))
	return filepath.Join(root, "docs", "plans", "2026-05-28-step-token-metrics.md")
}

func (s *stepTokenMetricsCtx) theStepTokenMetricsDesignDoc() error {
	s.docPath = tokenMetricsDocPath()
	data, err := os.ReadFile(s.docPath)
	if err != nil {
		s.docContent = ""
		s.docLoaded = false
		return nil
	}
	s.docContent = string(data)
	s.docLoaded = true
	return nil
}

// ─── L1: Schema and storage design steps ─────────────────────────────────────

func (s *stepTokenMetricsCtx) theStepTokenMetricsDesignDocFileExists() error {
	if _, err := os.Stat(s.docPath); err != nil {
		return fmt.Errorf("design doc not found at %s: %w", s.docPath, err)
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDesignDocHasStepIdentifierSection() error {
	return errors.New("pending: L1 implementation — no Step Identifier section yet")
}

func (s *stepTokenMetricsCtx) stepIdentifierSectionDefinesCanonicalKey() error {
	return errors.New("pending: L1 implementation — canonical key not yet defined")
}

func (s *stepTokenMetricsCtx) stepIdentifierSectionReconcilesWithRunStateView() error {
	return errors.New("pending: L1 implementation — reconciliation with run-state-step-view not documented")
}

func (s *stepTokenMetricsCtx) theDesignDocHasSchemaSection() error {
	return errors.New("pending: L1 implementation — no Schema section yet")
}

func (s *stepTokenMetricsCtx) schemaIncludesInputTokensField() error {
	return errors.New("pending: L1 implementation — input_tokens field not yet in schema")
}

func (s *stepTokenMetricsCtx) schemaIncludesOutputTokensField() error {
	return errors.New("pending: L1 implementation — output_tokens field not yet in schema")
}

func (s *stepTokenMetricsCtx) schemaIncludesTimestampField() error {
	return errors.New("pending: L1 implementation — timestamp field not yet in schema")
}

func (s *stepTokenMetricsCtx) schemaIncludesWorkflowNameScopeField() error {
	return errors.New("pending: L1 implementation — workflow name scope field not yet in schema")
}

func (s *stepTokenMetricsCtx) schemaIncludesStepNameScopeField() error {
	return errors.New("pending: L1 implementation — step name scope field not yet in schema")
}

func (s *stepTokenMetricsCtx) theDesignDocHasHostVsContainerSection() error {
	return errors.New("pending: L1 implementation — no Host vs Container section yet")
}

func (s *stepTokenMetricsCtx) hostVsContainerSectionConfirmsOrDocumentsGaps() error {
	return errors.New("pending: L1 implementation — host vs container coverage not documented")
}

// ─── L2: Query shapes and CLI surface steps ───────────────────────────────────

func (s *stepTokenMetricsCtx) theDesignDocHasQueryShapesSection() error {
	return errors.New("pending: L2 implementation — no Query Shapes section yet")
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesSliceByStep() error {
	return errors.New("pending: L2 implementation — slice-by-step query not yet specified")
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesAggregateByWorkflow() error {
	return errors.New("pending: L2 implementation — aggregate-by-workflow query not yet specified")
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesTrendOverTime() error {
	return errors.New("pending: L2 implementation — trend-over-time query not yet specified")
}

func (s *stepTokenMetricsCtx) theDesignDocHasCLISurfaceSection() error {
	return errors.New("pending: L2 implementation — no CLI Surface section yet")
}

func (s *stepTokenMetricsCtx) cliSurfaceSectionNamesMetricsCommand() error {
	return errors.New("pending: L2 implementation — cloche metrics command not yet documented")
}

func (s *stepTokenMetricsCtx) theDesignDocHasStorageSection() error {
	return errors.New("pending: L2 implementation — no Storage section yet")
}

func (s *stepTokenMetricsCtx) storageSectionReferencesMetricsTable() error {
	return errors.New("pending: L2 implementation — storage section not reconciled with metrics-reporting design")
}

func (s *stepTokenMetricsCtx) noUnresolvedOpenQuestions() error {
	return errors.New("pending: L2 implementation — open questions not yet resolved in design doc")
}

// ─── Step registration ────────────────────────────────────────────────────────

func init() { registerScenarios(initStepTokenMetricsScenarios) }

func initStepTokenMetricsScenarios(ctx *godog.ScenarioContext) {
	s := &stepTokenMetricsCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	ctx.Step(`^the step-token-metrics design doc$`, s.theStepTokenMetricsDesignDoc)

	// L1: Schema and storage design
	ctx.Step(`^the step-token-metrics design doc file exists$`, s.theStepTokenMetricsDesignDocFileExists)
	ctx.Step(`^the design doc has a step identifier section$`, s.theDesignDocHasStepIdentifierSection)
	ctx.Step(`^the step identifier section defines the canonical key as workflow name plus step name$`, s.stepIdentifierSectionDefinesCanonicalKey)
	ctx.Step(`^the step identifier section reconciles with the run-state step-view design$`, s.stepIdentifierSectionReconcilesWithRunStateView)
	ctx.Step(`^the design doc has a schema section$`, s.theDesignDocHasSchemaSection)
	ctx.Step(`^the schema includes an input_tokens field$`, s.schemaIncludesInputTokensField)
	ctx.Step(`^the schema includes an output_tokens field$`, s.schemaIncludesOutputTokensField)
	ctx.Step(`^the schema includes a timestamp field$`, s.schemaIncludesTimestampField)
	ctx.Step(`^the schema includes a workflow name scope field$`, s.schemaIncludesWorkflowNameScopeField)
	ctx.Step(`^the schema includes a step name scope field$`, s.schemaIncludesStepNameScopeField)
	ctx.Step(`^the design doc has a host vs container section$`, s.theDesignDocHasHostVsContainerSection)
	ctx.Step(`^the host vs container section confirms or documents gaps in coverage$`, s.hostVsContainerSectionConfirmsOrDocumentsGaps)

	// L2: Query shapes and CLI surface
	ctx.Step(`^the design doc has a query shapes section$`, s.theDesignDocHasQueryShapesSection)
	ctx.Step(`^the query shapes section includes a slice-by-step query with a concrete example$`, s.queryShapesSectionIncludesSliceByStep)
	ctx.Step(`^the query shapes section includes an aggregate-by-workflow query with a concrete example$`, s.queryShapesSectionIncludesAggregateByWorkflow)
	ctx.Step(`^the query shapes section includes a trend-over-time query with a concrete example$`, s.queryShapesSectionIncludesTrendOverTime)
	ctx.Step(`^the design doc has a CLI surface section$`, s.theDesignDocHasCLISurfaceSection)
	ctx.Step(`^the CLI surface section names a cloche metrics command$`, s.cliSurfaceSectionNamesMetricsCommand)
	ctx.Step(`^the design doc has a storage section$`, s.theDesignDocHasStorageSection)
	ctx.Step(`^the storage section references the metrics table from the metrics-reporting design$`, s.storageSectionReferencesMetricsTable)
	ctx.Step(`^the design doc has no unresolved open questions$`, s.noUnresolvedOpenQuestions)
}
