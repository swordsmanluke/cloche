package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

type resumeRebuildCtx struct{}

func (s *resumeRebuildCtx) reset() { *s = resumeRebuildCtx{} }

// ── L1: Snapshot capture ──────────────────────────────────────────────────────

func (s *resumeRebuildCtx) aClocheRunIsExecutingInAContainer() error {
	return errors.New("pending: L1 snapshot-capture implementation")
}

func (s *resumeRebuildCtx) aStepCompletesSuccessfully() error {
	return errors.New("pending: L1 snapshot-capture implementation")
}

func (s *resumeRebuildCtx) aWorkspaceSnapshotIsSavedForThatStep() error {
	return errors.New("pending: L1 snapshot-capture implementation")
}

func (s *resumeRebuildCtx) theSnapshotIncludesModifiedAndNewFiles() error {
	return errors.New("pending: L1 snapshot-capture implementation")
}

// ── L2: CLI flag acceptance ───────────────────────────────────────────────────

func (s *resumeRebuildCtx) aTaskHasAPriorFailedRunWithAtLeastOneSuccessfulStep() error {
	return errors.New("pending: L2 CLI-flags implementation")
}

func (s *resumeRebuildCtx) theUserRunsResumeNoRebuildForThatTask() error {
	return errors.New("pending: L2 CLI-flags implementation")
}

func (s *resumeRebuildCtx) theUserRunsResumeCleanForThatTask() error {
	return errors.New("pending: L2 CLI-flags implementation")
}

func (s *resumeRebuildCtx) theCommandIsAcceptedWithoutError() error {
	return errors.New("pending: L2 CLI-flags implementation")
}

// ── L3: Rebuild behavior ──────────────────────────────────────────────────────

func (s *resumeRebuildCtx) aTaskHasAPriorFailedRunWithSnapshotAfterStep(stepName string) error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theUserRunsResumeForThatTask() error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theDaemonBuildsAFreshContainer() error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theWorkspaceSnapshotFromStepIsApplied(stepName string) error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theFailedStepIsRetriedInsideTheFreshContainer() error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theDaemonDoesNotRebuildTheContainer() error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) theWorkspaceSnapshotFromStepIsAppliedBeforeRetry(stepName string) error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

func (s *resumeRebuildCtx) noWorkspaceSnapshotIsAppliedToTheContainer() error {
	return errors.New("pending: L3 rebuild-and-restore implementation")
}

// aliased: same assertion, reused across --clean and no-prior-snapshot scenarios

// ── L3: Multi-attempt selection ───────────────────────────────────────────────

func (s *resumeRebuildCtx) aTaskHasTwoPriorAttempts() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) theFirstAttemptHasCompletedStepSuccessfully(stepName string) error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) theSecondAttemptHasNoSuccessfulSteps() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) theDaemonUsesSnapshotFromFirstAttempt() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) aTaskHasTwoPriorAttemptsEachWithSuccessfulStep() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) theUserRunsResumeWithAttemptFlag() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) theDaemonUsesSnapshotFromSpecifiedAttempt() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

func (s *resumeRebuildCtx) aTaskHasAPriorAttemptWithNoSuccessfulSteps() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}


func (s *resumeRebuildCtx) theRunStartsFromTheFirstStep() error {
	return errors.New("pending: L3 multi-attempt-selection implementation")
}

// ── L3: Conflict resolution ───────────────────────────────────────────────────

func (s *resumeRebuildCtx) aWorkspaceSnapshotContainsChangesToAFileThatTheRebuiltImageAlsoModifies() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

func (s *resumeRebuildCtx) theSnapshotIsAppliedToTheFreshContainer() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

func (s *resumeRebuildCtx) theFileInTheContainerReflectsTheAgentVersion() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

func (s *resumeRebuildCtx) aWorkspaceSnapshotHasAnUnresolvableConflict() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

func (s *resumeRebuildCtx) theRunFailsBeforeTheAgentStepIsDispatched() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

func (s *resumeRebuildCtx) theErrorOutputNamesTheConflictingFile() error {
	return errors.New("pending: L3 conflict-resolution implementation")
}

// ── Step registration ─────────────────────────────────────────────────────────

func initResumeRebuildScenarios(ctx *godog.ScenarioContext) {
	s := &resumeRebuildCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// L1: Snapshot capture
	ctx.Step(`^a cloche run is executing in a container$`, s.aClocheRunIsExecutingInAContainer)
	ctx.Step(`^a step completes successfully$`, s.aStepCompletesSuccessfully)
	ctx.Step(`^a workspace snapshot is saved for that step under the run record$`, s.aWorkspaceSnapshotIsSavedForThatStep)
	ctx.Step(`^the snapshot includes modified and new files relative to the initial image$`, s.theSnapshotIncludesModifiedAndNewFiles)

	// L2: CLI flag acceptance
	ctx.Step(`^a task has a prior failed run with at least one successful step$`, s.aTaskHasAPriorFailedRunWithAtLeastOneSuccessfulStep)
	ctx.Step(`^the user runs "cloche resume --no-rebuild" for that task$`, s.theUserRunsResumeNoRebuildForThatTask)
	ctx.Step(`^the user runs "cloche resume --clean" for that task$`, s.theUserRunsResumeCleanForThatTask)
	ctx.Step(`^the command is accepted without error$`, s.theCommandIsAcceptedWithoutError)

	// L3: Rebuild behavior
	ctx.Step(`^a task has a prior failed run with a workspace snapshot after step "([^"]*)"$`, s.aTaskHasAPriorFailedRunWithSnapshotAfterStep)
	ctx.Step(`^the user runs "cloche resume" for that task$`, s.theUserRunsResumeForThatTask)
	ctx.Step(`^the daemon builds a fresh container from the project Dockerfile$`, s.theDaemonBuildsAFreshContainer)
	ctx.Step(`^the workspace snapshot from step "([^"]*)" is applied to the container$`, s.theWorkspaceSnapshotFromStepIsApplied)
	ctx.Step(`^the failed step is retried inside the fresh container$`, s.theFailedStepIsRetriedInsideTheFreshContainer)
	ctx.Step(`^the daemon does not rebuild the container$`, s.theDaemonDoesNotRebuildTheContainer)
	ctx.Step(`^the workspace snapshot from step "([^"]*)" is applied before retry$`, s.theWorkspaceSnapshotFromStepIsAppliedBeforeRetry)
	ctx.Step(`^no workspace snapshot is applied to the container$`, s.noWorkspaceSnapshotIsAppliedToTheContainer)

	// L3: Multi-attempt selection
	ctx.Step(`^a task has two prior attempts$`, s.aTaskHasTwoPriorAttempts)
	ctx.Step(`^the first attempt has completed step "([^"]*)" successfully$`, s.theFirstAttemptHasCompletedStepSuccessfully)
	ctx.Step(`^the second attempt has no successful steps$`, s.theSecondAttemptHasNoSuccessfulSteps)
	ctx.Step(`^the daemon uses the workspace snapshot from the first attempt$`, s.theDaemonUsesSnapshotFromFirstAttempt)
	ctx.Step(`^a task has two prior attempts each with at least one successful step$`, s.aTaskHasTwoPriorAttemptsEachWithSuccessfulStep)
	ctx.Step(`^the user runs "cloche resume --attempt <first-run-id>" for that task$`, s.theUserRunsResumeWithAttemptFlag)
	ctx.Step(`^the daemon uses the workspace snapshot from that specified attempt$`, s.theDaemonUsesSnapshotFromSpecifiedAttempt)
	ctx.Step(`^a task has a prior attempt with no successful steps$`, s.aTaskHasAPriorAttemptWithNoSuccessfulSteps)
	ctx.Step(`^the run starts from the first step$`, s.theRunStartsFromTheFirstStep)

	// L3: Conflict resolution
	ctx.Step(`^a workspace snapshot contains changes to a file that the rebuilt image also modifies$`, s.aWorkspaceSnapshotContainsChangesToAFileThatTheRebuiltImageAlsoModifies)
	ctx.Step(`^the snapshot is applied to the fresh container$`, s.theSnapshotIsAppliedToTheFreshContainer)
	ctx.Step(`^the file in the container reflects the agent's version$`, s.theFileInTheContainerReflectsTheAgentVersion)
	ctx.Step(`^a workspace snapshot has an unresolvable conflict with a file in the rebuilt image$`, s.aWorkspaceSnapshotHasAnUnresolvableConflict)
	ctx.Step(`^the run fails before the agent step is dispatched$`, s.theRunFailsBeforeTheAgentStepIsDispatched)
	ctx.Step(`^the error output names the conflicting file$`, s.theErrorOutputNamesTheConflictingFile)
}
