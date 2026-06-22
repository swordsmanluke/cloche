package features_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"

	"github.com/cucumber/godog"
)

type resumeRebuildDesignCtx struct {
	docPath string
}

func (s *resumeRebuildDesignCtx) reset() {
	// rootDir: .../repos/cloche/features/ → .../repos/cloche/ → .../repos/ → workspace root
	_, thisFile, _, _ := runtime.Caller(0)
	reposCloche := filepath.Dir(filepath.Dir(thisFile))
	workspaceRoot := filepath.Dir(filepath.Dir(reposCloche))
	s.docPath = filepath.Join(workspaceRoot, "docs", "plans", "2026-05-28-resume-rebuild-preserve-workspace.md")
}

// ─── Given ────────────────────────────────────────────────────────────────────

func (s *resumeRebuildDesignCtx) theResumeRebuildDesignDocumentExists() error {
	return errors.New("pending: L1 implementation")
}

// ─── Then: status ─────────────────────────────────────────────────────────────

func (s *resumeRebuildDesignCtx) theDesignDocumentStatusIsAtLeast(_ string) error {
	return errors.New("pending: L1 implementation")
}

func (s *resumeRebuildDesignCtx) theDesignDocumentStatusIs(_ string) error {
	return errors.New("pending: L2 implementation")
}

// ─── Then: sections ───────────────────────────────────────────────────────────

func (s *resumeRebuildDesignCtx) theDesignDocumentHasASection(_ string) error {
	return errors.New("pending: L1 implementation")
}

func (s *resumeRebuildDesignCtx) theSectionContainsConcreteChoiceBetweenGitBranchAndSnapshot() error {
	return errors.New("pending: L1 implementation")
}

func (s *resumeRebuildDesignCtx) theSectionDefinesWhichFilesTakePrecedenceWhenConflictsOccur() error {
	return errors.New("pending: L1 implementation")
}

func (s *resumeRebuildDesignCtx) theSectionStatesTheDefaultBehaviorWhenMultipleFailedAttemptsExist() error {
	return errors.New("pending: L1 implementation")
}

func (s *resumeRebuildDesignCtx) theSectionStatesWhetherRebuildPreserveIsDefaultOrRequiresFlag() error {
	return errors.New("pending: L1 implementation")
}

// ─── Then: L2 quality gates ──────────────────────────────────────────────────

func (s *resumeRebuildDesignCtx) theDesignDocumentContainsNoUnresolvedPlaceholderText() error {
	return errors.New("pending: L2 implementation")
}

func (s *resumeRebuildDesignCtx) theImplementationNotesHaveASection(_ string) error {
	return errors.New("pending: L2 implementation")
}

func (s *resumeRebuildDesignCtx) theImplementationNotesReference(_ string) error {
	return errors.New("pending: L2 implementation")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initResumeRebuildDesignScenarios(ctx *godog.ScenarioContext) {
	s := &resumeRebuildDesignCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^the resume-rebuild design document exists$`, s.theResumeRebuildDesignDocumentExists)

	// Status checks
	ctx.Step(`^the design document status is at least "([^"]*)"$`, s.theDesignDocumentStatusIsAtLeast)
	ctx.Step(`^the design document status is "([^"]*)"$`, s.theDesignDocumentStatusIs)

	// Section checks (L1)
	ctx.Step(`^the design document has a "([^"]*)" section$`, s.theDesignDocumentHasASection)
	ctx.Step(`^the section contains a concrete choice between git branch and snapshot$`, s.theSectionContainsConcreteChoiceBetweenGitBranchAndSnapshot)
	ctx.Step(`^the section defines which files take precedence when conflicts occur$`, s.theSectionDefinesWhichFilesTakePrecedenceWhenConflictsOccur)
	ctx.Step(`^the section states the default behavior when multiple failed attempts exist$`, s.theSectionStatesTheDefaultBehaviorWhenMultipleFailedAttemptsExist)
	ctx.Step(`^the section states whether rebuild-preserve is the default or requires an explicit flag$`, s.theSectionStatesWhetherRebuildPreserveIsDefaultOrRequiresFlag)

	// L2 quality gates
	ctx.Step(`^the design document contains no unresolved placeholder text$`, s.theDesignDocumentContainsNoUnresolvedPlaceholderText)
	ctx.Step(`^the design document has an "([^"]*)" section$`, s.theImplementationNotesHaveASection)
	ctx.Step(`^the implementation notes reference "([^"]*)"$`, s.theImplementationNotesReference)
}
