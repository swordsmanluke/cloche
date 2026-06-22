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

// stepTokenMetricsCtx holds per-scenario state for step-token-metrics BDD scenarios.
type stepTokenMetricsCtx struct {
	content string
	docErr  error
}

func (s *stepTokenMetricsCtx) reset() {
	*s = stepTokenMetricsCtx{}
}

// stepTokenMetricsDocPath returns the absolute path of the design doc.
func stepTokenMetricsDocPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile: .../repos/cloche/features/step_token_metrics_test.go
	// up one level: features → repos/cloche
	root := filepath.Dir(filepath.Dir(thisFile))
	return filepath.Join(root, "docs", "plans", "2026-05-28-step-token-metrics.md")
}

// sectionPresent checks whether a markdown heading (any level) with the given
// text exists in content. Comparison is case-insensitive.
func sectionPresent(content, heading string) bool {
	lower := strings.ToLower(content)
	h := strings.ToLower(heading)
	return strings.Contains(lower, "\n# "+h) ||
		strings.Contains(lower, "\n## "+h) ||
		strings.Contains(lower, "\n### "+h) ||
		strings.HasPrefix(lower, "# "+h) ||
		strings.HasPrefix(lower, "## "+h) ||
		strings.HasPrefix(lower, "### "+h)
}

// ─── Given ───────────────────────────────────────────────────────────────────

// theStepTokenMetricsDesignDoc attempts to read the design doc and stores the
// result. It always succeeds so that Then steps can report their own errors.
func (s *stepTokenMetricsCtx) theStepTokenMetricsDesignDoc() error {
	data, err := os.ReadFile(stepTokenMetricsDocPath())
	s.docErr = err
	if err == nil {
		s.content = string(data)
	}
	return nil
}

// ─── L1: Then steps ──────────────────────────────────────────────────────────

func (s *stepTokenMetricsCtx) theDocFileExists() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L1 implementation — %w", s.docErr)
	}
	return nil
}

func (s *stepTokenMetricsCtx) theDocContainsASection(heading string) error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L1 implementation — doc not yet created: %w", s.docErr)
	}
	if !sectionPresent(s.content, heading) {
		return fmt.Errorf("pending: L1 implementation — section %q not yet present in doc", heading)
	}
	return nil
}

func (s *stepTokenMetricsCtx) theStepIdentifierSectionNamesCanonicalKeyFields() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L1 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L1 implementation — Step Identifier section canonical key not yet resolved")
}

func (s *stepTokenMetricsCtx) theSchemaSectionListsTokenFields() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L1 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L1 implementation — Schema section field list not yet written")
}

// ─── L2: Then steps ──────────────────────────────────────────────────────────

func (s *stepTokenMetricsCtx) theDocContainsSliceByStepQueryExample() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — slice-by-step query example not yet present")
}

func (s *stepTokenMetricsCtx) theDocContainsAggregateByWorkflowQueryExample() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — aggregate-by-workflow query example not yet present")
}

func (s *stepTokenMetricsCtx) theDocContainsTrendOverTimeQueryExample() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — trend-over-time query example not yet present")
}

func (s *stepTokenMetricsCtx) theCLISectionReferencesCommands() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — CLI section command forms not yet written")
}

func (s *stepTokenMetricsCtx) theStorageSectionReferencesMetricsTable() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — Storage section metrics table reference not yet present")
}

func (s *stepTokenMetricsCtx) theDocHasNoUnresolvedOpenQuestions() error {
	if s.docErr != nil {
		return fmt.Errorf("pending: L2 implementation — doc not yet created: %w", s.docErr)
	}
	return errors.New("pending: L2 implementation — open questions may remain unresolved")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initStepTokenMetricsScenarios(ctx *godog.ScenarioContext) {
	s := &stepTokenMetricsCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Given
	ctx.Step(`^the step-token-metrics design doc$`, s.theStepTokenMetricsDesignDoc)

	// L1: Then
	ctx.Step(`^the doc file exists$`, s.theDocFileExists)
	ctx.Step(`^the doc contains a "([^"]*)" section$`, s.theDocContainsASection)
	ctx.Step(`^the "Step Identifier" section names the canonical key fields$`, s.theStepIdentifierSectionNamesCanonicalKeyFields)
	ctx.Step(`^the "Schema" section lists "input_tokens" and "output_tokens" fields$`, s.theSchemaSectionListsTokenFields)

	// L2: Then
	ctx.Step(`^the doc contains a slice-by-step query example$`, s.theDocContainsSliceByStepQueryExample)
	ctx.Step(`^the doc contains an aggregate-by-workflow query example$`, s.theDocContainsAggregateByWorkflowQueryExample)
	ctx.Step(`^the doc contains a trend-over-time query example$`, s.theDocContainsTrendOverTimeQueryExample)
	ctx.Step(`^the "CLI" section references "cloche metrics" or "clo metric"$`, s.theCLISectionReferencesCommands)
	ctx.Step(`^the "Storage" section references the metrics table$`, s.theStorageSectionReferencesMetricsTable)
	ctx.Step(`^the doc has no unresolved open questions$`, s.theDocHasNoUnresolvedOpenQuestions)
}
