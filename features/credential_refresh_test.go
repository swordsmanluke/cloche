package features_test

import (
	"errors"
	"testing"

	"github.com/cucumber/godog"
)

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

func initializeScenario(ctx *godog.ScenarioContext) {
	// Background
	ctx.Step(`^the cloche daemon is running$`, theClocheDAemonIsRunning)

	// Scenario: Agent step authenticates on container startup
	ctx.Step(`^the host has a valid credentials file$`, theHostHasAValidCredentialsFile)
	ctx.Step(`^a cloche attempt is started for a project$`, aClocheAttemptIsStarted)
	ctx.Step(`^an agent step executes inside the container$`, anAgentStepExecutes)
	ctx.Step(`^the step completes without an authentication error$`, stepCompletesWithoutAuthError)

	// Scenario: Re-authenticates after atomic rename
	ctx.Step(`^a cloche attempt is in progress with at least one completed agent step$`, attemptInProgressWithCompletedStep)
	ctx.Step(`^the host atomically replaces its credentials file via rename$`, hostAtomicallyReplacesCreds)
	ctx.Step(`^another agent step executes inside the same container$`, anotherAgentStepExecutes)

	// Scenario: Staging directory removed on stop
	ctx.Step(`^a cloche attempt has finished and the container has been stopped$`, attemptFinishedContainerStopped)
	ctx.Step(`^no cloche credential staging directories remain on the host$`, noStagingDirsRemain)

	// Scenario: Watcher failure is visible
	ctx.Step(`^the host credentials directory cannot be watched by fsnotify$`, credsDirNotWatchable)
	ctx.Step(`^the daemon starts a container for a new attempt$`, daemonStartsContainer)
	ctx.Step(`^a warning log entry identifies the affected container by ID$`, warningIdentifiesContainerID)
	ctx.Step(`^the daemon does not silently fall back to the old single-file bind-mount$`, noSilentFallbackToSingleFile)
}

// Background step

func theClocheDAemonIsRunning() error {
	return errors.New("pending: L1 implementation")
}

// Scenario 1: Authentication on startup

func theHostHasAValidCredentialsFile() error {
	return errors.New("pending: L1 implementation")
}

func aClocheAttemptIsStarted() error {
	return errors.New("pending: L1 implementation")
}

func anAgentStepExecutes() error {
	return errors.New("pending: L1 implementation")
}

func stepCompletesWithoutAuthError() error {
	return errors.New("pending: L1 implementation")
}

// Scenario 2: Re-authentication after atomic rename

func attemptInProgressWithCompletedStep() error {
	return errors.New("pending: L1 implementation")
}

func hostAtomicallyReplacesCreds() error {
	return errors.New("pending: L1 implementation")
}

func anotherAgentStepExecutes() error {
	return errors.New("pending: L1 implementation")
}

// Scenario 3: Staging directory cleanup

func attemptFinishedContainerStopped() error {
	return errors.New("pending: L2 implementation")
}

func noStagingDirsRemain() error {
	return errors.New("pending: L2 implementation")
}

// Scenario 4: Watcher failure visibility

func credsDirNotWatchable() error {
	return errors.New("pending: L2 implementation")
}

func daemonStartsContainer() error {
	return errors.New("pending: L2 implementation")
}

func warningIdentifiesContainerID() error {
	return errors.New("pending: L2 implementation")
}

func noSilentFallbackToSingleFile() error {
	return errors.New("pending: L2 implementation")
}
