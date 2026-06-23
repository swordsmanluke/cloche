package features_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	if !strings.Contains(s.docContent, "## Step Identifier") {
		return errors.New("design doc has no '## Step Identifier' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) stepIdentifierSectionDefinesCanonicalKey() error {
	if !strings.Contains(s.docContent, "workflow_name") || !strings.Contains(s.docContent, "step_name") {
		return errors.New("step identifier section does not define canonical key as (workflow_name, step_name)")
	}
	return nil
}

func (s *stepTokenMetricsCtx) stepIdentifierSectionReconcilesWithRunStateView() error {
	if !strings.Contains(s.docContent, "run-state-step-view") {
		return errors.New("step identifier section does not reference run-state-step-view design")
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDesignDocHasSchemaSection() error {
	if !strings.Contains(s.docContent, "## Schema") {
		return errors.New("design doc has no '## Schema' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) schemaIncludesInputTokensField() error {
	if !strings.Contains(s.docContent, "input_tokens") {
		return errors.New("schema does not include input_tokens field")
	}
	return nil
}

func (s *stepTokenMetricsCtx) schemaIncludesOutputTokensField() error {
	if !strings.Contains(s.docContent, "output_tokens") {
		return errors.New("schema does not include output_tokens field")
	}
	return nil
}

func (s *stepTokenMetricsCtx) schemaIncludesTimestampField() error {
	if !strings.Contains(s.docContent, "timestamp") {
		return errors.New("schema does not include timestamp field")
	}
	return nil
}

func (s *stepTokenMetricsCtx) schemaIncludesWorkflowNameScopeField() error {
	if !strings.Contains(s.docContent, "workflow_name") {
		return errors.New("schema does not include workflow_name scope field")
	}
	return nil
}

func (s *stepTokenMetricsCtx) schemaIncludesStepNameScopeField() error {
	if !strings.Contains(s.docContent, "step_name") {
		return errors.New("schema does not include step_name scope field")
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDesignDocHasHostVsContainerSection() error {
	if !strings.Contains(s.docContent, "## Host vs Container") {
		return errors.New("design doc has no '## Host vs Container' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) hostVsContainerSectionConfirmsOrDocumentsGaps() error {
	hasConfirmation := strings.Contains(s.docContent, "Coverage is complete") ||
		strings.Contains(s.docContent, "coverage is complete") ||
		strings.Contains(s.docContent, "Gap")
	if !hasConfirmation {
		return errors.New("host vs container section does not confirm coverage or document gaps")
	}
	return nil
}

// ─── L2: Query shapes and CLI surface steps ───────────────────────────────────

func (s *stepTokenMetricsCtx) theDesignDocHasQueryShapesSection() error {
	if !strings.Contains(s.docContent, "## Query Shapes") {
		return errors.New("design doc has no '## Query Shapes' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesSliceByStep() error {
	if !strings.Contains(s.docContent, "slice-by-step") && !strings.Contains(s.docContent, "slice by step") {
		return errors.New("query shapes section does not include a slice-by-step query")
	}
	return nil
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesAggregateByWorkflow() error {
	if !strings.Contains(s.docContent, "aggregate-by-workflow") && !strings.Contains(s.docContent, "aggregate by workflow") {
		return errors.New("query shapes section does not include an aggregate-by-workflow query")
	}
	return nil
}

func (s *stepTokenMetricsCtx) queryShapesSectionIncludesTrendOverTime() error {
	if !strings.Contains(s.docContent, "trend-over-time") && !strings.Contains(s.docContent, "trend over time") {
		return errors.New("query shapes section does not include a trend-over-time query")
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDesignDocHasCLISurfaceSection() error {
	if !strings.Contains(s.docContent, "## CLI Surface") {
		return errors.New("design doc has no '## CLI Surface' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) cliSurfaceSectionNamesMetricsCommand() error {
	if !strings.Contains(s.docContent, "cloche metrics") {
		return errors.New("CLI surface section does not name a 'cloche metrics' command")
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDesignDocHasStorageSection() error {
	if !strings.Contains(s.docContent, "## Storage") {
		return errors.New("design doc has no '## Storage' section")
	}
	return nil
}

func (s *stepTokenMetricsCtx) storageSectionReferencesMetricsTable() error {
	if !strings.Contains(s.docContent, "metrics") {
		return errors.New("storage section does not reference the metrics table")
	}
	return nil
}

func (s *stepTokenMetricsCtx) noUnresolvedOpenQuestions() error {
	if strings.Contains(s.docContent, "## Open Questions") {
		return errors.New("design doc still has an unresolved 'Open Questions' section")
	}
	return nil
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
