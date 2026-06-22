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

// runStateStepViewCtx holds per-scenario state for the run-state step-view design doc scenarios.
type runStateStepViewCtx struct {
	docPath    string
	docContent string
	docErr     error
}

func (s *runStateStepViewCtx) reset() {
	*s = runStateStepViewCtx{}
}

// repoRoot returns the path to the repos/cloche project root (two levels up from this file).
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile: .../repos/cloche/features/run_state_step_view_test.go
	return filepath.Dir(filepath.Dir(thisFile))
}

// ─── Background ──────────────────────────────────────────────────────────────

func (s *runStateStepViewCtx) theRunStateStepViewDesignDocAt(path string) error {
	s.docPath = path
	data, err := os.ReadFile(filepath.Join(repoRoot(), path))
	if err != nil {
		s.docErr = fmt.Errorf("design doc not found at %s: %w", path, err)
		return nil // defer failure to scenario steps so godog reports them individually
	}
	s.docContent = string(data)
	return nil
}

// ─── L1: Data model steps ────────────────────────────────────────────────────

func (s *runStateStepViewCtx) theDesignDocFileExistsAndIsNonEmpty() error {
	if s.docErr != nil {
		return s.docErr
	}
	if strings.TrimSpace(s.docContent) == "" {
		return fmt.Errorf("design doc at %s is empty", s.docPath)
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocContainsASection(section string) error {
	if s.docErr != nil {
		return s.docErr
	}
	if !strings.Contains(s.docContent, section) {
		return fmt.Errorf("design doc does not contain a %q section", section)
	}
	return nil
}

func (s *runStateStepViewCtx) theRowSchemaNamesTheColumns(cols string) error {
	if s.docErr != nil {
		return s.docErr
	}
	for _, col := range strings.Split(cols, `", "`) {
		col = strings.Trim(col, `"`)
		if !strings.Contains(s.docContent, col) {
			return fmt.Errorf("design doc row schema does not mention column %q", col)
		}
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesStepFQNFormat(format string) error {
	if s.docErr != nil {
		return s.docErr
	}
	if !strings.Contains(s.docContent, format) {
		return fmt.Errorf("design doc does not specify FQN format %q", format)
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocNamesDataSourceEndpoint(endpoint string) error {
	if s.docErr != nil {
		return s.docErr
	}
	if !strings.Contains(s.docContent, endpoint) {
		return fmt.Errorf("design doc does not name endpoint %q", endpoint)
	}
	return nil
}

// ─── L2: UI presentation steps ───────────────────────────────────────────────

func (s *runStateStepViewCtx) theDesignDocSpecifiesOrderingBy(field string) error {
	if s.docErr != nil {
		return s.docErr
	}
	if !strings.Contains(s.docContent, field) {
		return fmt.Errorf("design doc does not specify chronological ordering by %q", field)
	}
	// also confirm chronological intent is present
	if !strings.Contains(strings.ToLower(s.docContent), "chronological") {
		return fmt.Errorf("design doc does not mention chronological ordering")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocDocumentsHandlingForPendingStepsWithNoField(field string) error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "pending") {
		return fmt.Errorf("design doc does not document handling for pending steps (no %q)", field)
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesHashBasedColorAssignment() error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "hash") {
		return fmt.Errorf("design doc does not specify a hash-based color assignment strategy")
	}
	if !strings.Contains(lower, "stable") && !strings.Contains(lower, "deterministic") {
		return fmt.Errorf("design doc does not indicate that color assignment is stable/deterministic")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocNamesCSSVariables(from, through string) error {
	if s.docErr != nil {
		return s.docErr
	}
	if !strings.Contains(s.docContent, from) {
		return fmt.Errorf("design doc does not name CSS variable %q", from)
	}
	if !strings.Contains(s.docContent, through) {
		return fmt.Errorf("design doc does not name CSS variable %q", through)
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocDocumentsPaletteWrappingBeyond6Segments() error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "wrap") && !strings.Contains(lower, "exhaust") {
		return fmt.Errorf("design doc does not document palette wrapping / exhaustion for 7+ workflow segments")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesNestingStrategyUsingColorAndIndent() error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "indent") {
		return fmt.Errorf("design doc nesting strategy does not mention indent")
	}
	if !strings.Contains(lower, "color") {
		return fmt.Errorf("design doc nesting strategy does not mention color")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesTruncationForLongQualifiedNames() error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "truncat") {
		return fmt.Errorf("design doc does not specify truncation for long qualified names")
	}
	return nil
}

func (s *runStateStepViewCtx) theDesignDocSpecifiesHoverTitleAttributeForFullNames() error {
	if s.docErr != nil {
		return s.docErr
	}
	lower := strings.ToLower(s.docContent)
	if !strings.Contains(lower, "title") {
		return fmt.Errorf("design doc does not specify a title attribute for full qualified names on hover")
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

	// Background
	ctx.Step(`^the run-state step-view design doc at "([^"]*)"$`, s.theRunStateStepViewDesignDocAt)

	// L1: Data model
	ctx.Step(`^the design doc file exists and is non-empty$`, s.theDesignDocFileExistsAndIsNonEmpty)
	ctx.Step(`^the design doc contains a "([^"]*)" section$`, s.theDesignDocContainsASection)
	ctx.Step(`^the row schema names the columns "([^"]*)"$`, s.theRowSchemaNamesTheColumns)
	ctx.Step(`^the design doc specifies the step FQN format as "([^"]*)"$`, s.theDesignDocSpecifiesStepFQNFormat)
	ctx.Step(`^the design doc names "([^"]*)" as the data source endpoint$`, s.theDesignDocNamesDataSourceEndpoint)

	// L2: UI presentation
	ctx.Step(`^the design doc specifies step ordering as chronological by "([^"]*)"$`, s.theDesignDocSpecifiesOrderingBy)
	ctx.Step(`^the design doc documents handling for pending steps with no "([^"]*)"$`, s.theDesignDocDocumentsHandlingForPendingStepsWithNoField)
	ctx.Step(`^the design doc specifies a hash-based color assignment strategy$`, s.theDesignDocSpecifiesHashBasedColorAssignment)
	ctx.Step(`^the design doc names CSS variables "([^"]*)" through "([^"]*)"$`, s.theDesignDocNamesCSSVariables)
	ctx.Step(`^the design doc documents color palette wrapping beyond 6 workflow segments$`, s.theDesignDocDocumentsPaletteWrappingBeyond6Segments)
	ctx.Step(`^the design doc specifies a nesting strategy using color and indent$`, s.theDesignDocSpecifiesNestingStrategyUsingColorAndIndent)
	ctx.Step(`^the design doc specifies truncation for long qualified names$`, s.theDesignDocSpecifiesTruncationForLongQualifiedNames)
	ctx.Step(`^the design doc specifies a hover title attribute for full qualified names$`, s.theDesignDocSpecifiesHoverTitleAttributeForFullNames)
}
