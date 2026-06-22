package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

type loopResumeGateCtx struct {
	commandOutput string
	commandErr    error
	loopRunning   bool
	resumableRuns int
	inFlightRun   bool
	restarted     bool
}

func (s *loopResumeGateCtx) reset() {
	*s = loopResumeGateCtx{}
}

// ─── Given steps ─────────────────────────────────────────────────────────────

func (s *loopResumeGateCtx) theOrchestrationLoopIsStopped() error {
	s.loopRunning = false
	return nil
}

func (s *loopResumeGateCtx) theOrchestrationLoopIsRunning() error {
	s.loopRunning = true
	return nil
}

func (s *loopResumeGateCtx) theClocheGateDaemonIsRunning() error {
	return errors.New("pending: L1 implementation")
}

func (s *loopResumeGateCtx) thereAreNResumableRuns(count int) error {
	s.resumableRuns = count
	return nil
}

func (s *loopResumeGateCtx) thereAreNoResumableRuns() error {
	s.resumableRuns = 0
	return nil
}

func (s *loopResumeGateCtx) aRunWasInFlightAtShutdown() error {
	s.inFlightRun = true
	return errors.New("pending: L2 implementation")
}

func (s *loopResumeGateCtx) aTaskIsDispatchedToTheLoop() error {
	return errors.New("pending: L2 implementation")
}

// ─── When steps ──────────────────────────────────────────────────────────────

func (s *loopResumeGateCtx) theOperatorRuns(cmd string) error {
	return errors.New("pending: L1 implementation")
}

func (s *loopResumeGateCtx) theDaemonIsRestarted() error {
	s.restarted = true
	return errors.New("pending: L2 implementation")
}

func (s *loopResumeGateCtx) theDispatchedRunCompletes() error {
	return errors.New("pending: L2 implementation")
}

// ─── Then steps ──────────────────────────────────────────────────────────────

func (s *loopResumeGateCtx) theLoopCommandSucceeds() error {
	return errors.New("pending: L1 implementation")
}

func (s *loopResumeGateCtx) theLoopCommandOutputContains(text string) error {
	return errors.New("pending: L1 implementation")
}

func (s *loopResumeGateCtx) theOrchestrationLoopIsNowStopped() error {
	return errors.New("pending: L1 implementation")
}

func (s *loopResumeGateCtx) theInFlightRunIsNotAutomaticallyResumed() error {
	return errors.New("pending: L2 implementation")
}

func (s *loopResumeGateCtx) theInFlightRunIsAutomaticallyResumed() error {
	return errors.New("pending: L2 implementation")
}

func (s *loopResumeGateCtx) noRunsAreAutomaticallyResumed() error {
	return errors.New("pending: L2 implementation")
}

func (s *loopResumeGateCtx) theDispatchedRunStatusIsSuccessful() error {
	return errors.New("pending: L2 implementation")
}

// ─── Scenario initializer ────────────────────────────────────────────────────

func initLoopResumeGateScenarios(ctx *godog.ScenarioContext) {
	s := &loopResumeGateCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Given
	ctx.Step(`^the orchestration loop is stopped$`, s.theOrchestrationLoopIsStopped)
	ctx.Step(`^the orchestration loop is running$`, s.theOrchestrationLoopIsRunning)
	ctx.Step(`^the cloche daemon is running$`, s.theClocheGateDaemonIsRunning)
	ctx.Step(`^there are (\d+) resumable runs$`, s.thereAreNResumableRuns)
	ctx.Step(`^there are no resumable runs$`, s.thereAreNoResumableRuns)
	ctx.Step(`^a run was in-flight when the daemon last shut down$`, s.aRunWasInFlightAtShutdown)
	ctx.Step(`^a task is dispatched to the loop$`, s.aTaskIsDispatchedToTheLoop)

	// When
	ctx.Step(`^the operator runs "([^"]*)"$`, s.theOperatorRuns)
	ctx.Step(`^the daemon is restarted$`, s.theDaemonIsRestarted)
	ctx.Step(`^the dispatched run completes$`, s.theDispatchedRunCompletes)

	// Then
	ctx.Step(`^the loop command succeeds$`, s.theLoopCommandSucceeds)
	ctx.Step(`^the loop command output contains "([^"]*)"$`, s.theLoopCommandOutputContains)
	ctx.Step(`^the orchestration loop is now stopped$`, s.theOrchestrationLoopIsNowStopped)
	ctx.Step(`^the in-flight run is not automatically resumed$`, s.theInFlightRunIsNotAutomaticallyResumed)
	ctx.Step(`^the in-flight run is automatically resumed$`, s.theInFlightRunIsAutomaticallyResumed)
	ctx.Step(`^no runs are automatically resumed$`, s.noRunsAreAutomaticallyResumed)
	ctx.Step(`^the dispatched run status is successful$`, s.theDispatchedRunStatusIsSuccessful)
}
