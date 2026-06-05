package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

// credentialsRefreshCtx holds per-scenario state for credentials-refresh BDD scenarios.
type credentialsRefreshCtx struct {
	// host setup
	claudeDir       string // temp ~/.claude dir on the host
	credentialsPath string // path of the credentials file in claudeDir

	// runtime under test (L1/L2: filled in by implementation)
	containerID  string
	stagingDir   string
	logOutput    string // captured daemon log lines

	// assertion helpers
	credContent string // content last read from container's credentials file
	warnings    []string
}

func (s *credentialsRefreshCtx) reset() {
	*s = credentialsRefreshCtx{}
}

// ─── Given ───────────────────────────────────────────────────────────────────

func (s *credentialsRefreshCtx) aTemporaryHostClaudeDirWithCredentials(content string) error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) hostAlsoContainsSettingsJSON() error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) aContainerIsRunning() error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) hostClaudeDirIsNotWatchable() error {
	return errors.New("pending: L2 runtime implementation")
}

// ─── When ────────────────────────────────────────────────────────────────────

func (s *credentialsRefreshCtx) aContainerIsStarted() error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) hostAtomicallyReplacesCredentials(content string) error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) containerIsStopped() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *credentialsRefreshCtx) containerTerminatesAbnormally() error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *credentialsRefreshCtx) nContainersStartedAndStopped(n int) error {
	return errors.New("pending: L2 runtime implementation")
}

// ─── Then ────────────────────────────────────────────────────────────────────

func (s *credentialsRefreshCtx) containerCanReadCredentials() error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) credentialsContentIs(expected string) error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) credentialsContentWithinTimeout(expected string) error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) stagedDirContainsSettingsJSON() error {
	return errors.New("pending: L1 runtime implementation")
}

func (s *credentialsRefreshCtx) noStagingDirRemains(pattern string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *credentialsRefreshCtx) noStagingDirsRemainMulti(pattern string) error {
	return errors.New("pending: L2 runtime implementation")
}

func (s *credentialsRefreshCtx) warningLoggedWithContainerID() error {
	return errors.New("pending: L2 runtime implementation")
}

// ─── Registration ─────────────────────────────────────────────────────────────

func initCredentialsRefreshScenarios(ctx *godog.ScenarioContext) {
	s := &credentialsRefreshCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^a temporary host \.claude directory with a credentials file containing "([^"]*)"$`, s.aTemporaryHostClaudeDirWithCredentials)

	// Given
	ctx.Step(`^the host \.claude directory also contains a settings\.json file$`, s.hostAlsoContainsSettingsJSON)
	ctx.Step(`^a container is running via the runtime$`, s.aContainerIsRunning)
	ctx.Step(`^the host \.claude directory is not watchable$`, s.hostClaudeDirIsNotWatchable)

	// When
	ctx.Step(`^a container is started via the runtime$`, s.aContainerIsStarted)
	ctx.Step(`^the host atomically replaces the credentials file with content "([^"]*)"$`, s.hostAtomicallyReplacesCredentials)
	ctx.Step(`^the container is stopped$`, s.containerIsStopped)
	ctx.Step(`^the container terminates without a clean stop call$`, s.containerTerminatesAbnormally)
	ctx.Step(`^(\d+) containers are started and stopped sequentially via the runtime$`, s.nContainersStartedAndStopped)

	// Then
	ctx.Step(`^the container can read the credentials file$`, s.containerCanReadCredentials)
	ctx.Step(`^the credentials content seen by the container is "([^"]*)"$`, s.credentialsContentIs)
	ctx.Step(`^within 2 seconds the container reads credentials content "([^"]*)"$`, s.credentialsContentWithinTimeout)
	ctx.Step(`^the container's staged directory contains a settings\.json file$`, s.stagedDirContainsSettingsJSON)
	ctx.Step(`^no staging directory matching "([^"]*)" remains under the system temp dir$`, s.noStagingDirRemains)
	ctx.Step(`^no staging directories matching "([^"]*)" remain under the system temp dir$`, s.noStagingDirsRemainMulti)
	ctx.Step(`^a warning is logged that mentions the container ID$`, s.warningLoggedWithContainerID)
}
