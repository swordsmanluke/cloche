package features_test

import (
	"context"
	"errors"

	"github.com/cucumber/godog"
)

// credentialsCtx holds per-scenario state for credentials bind-mount BDD scenarios.
type credentialsCtx struct {
	// set in Given steps
	sourceDir    string // fake ~/.claude/ directory on host
	canBeWatched bool   // controls whether fsnotify can watch sourceDir

	// set during When steps
	dockerArgs       []string // args that would be passed to docker run
	stagingDirs      []string // per-container staging dirs created by the runtime
	containerStarted bool
	containerStopped bool

	// updated credentials content
	updatedContent string

	// observable outcomes
	stagingContent   string // content read from staging dir after update
	contentPropagated bool
	warningLogged    bool
	warningMessage   string
}

func (s *credentialsCtx) reset() {
	*s = credentialsCtx{}
}

// ─── L1: staging directory + fsnotify watcher steps ─────────────────────────

func (s *credentialsCtx) aDockerRuntimeWithCredentialsSourceDir() error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) aContainerIsStarted() error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) aContainerHasBeenStarted() error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) dockerBindMountArgReferencesDirectory() error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) containerPathMountedIs(path string) error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) hostAtomicallyReplacesCredentials(filename string) error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) stagingDirReflectsNewContentWithinTimeout(seconds int) error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) twoContainersStartedConcurrently() error {
	return errors.New("pending: L1 docker runtime implementation")
}

func (s *credentialsCtx) eachContainerHasDistinctStagingDir() error {
	return errors.New("pending: L1 docker runtime implementation")
}

// ─── L2: hardening steps ─────────────────────────────────────────────────────

func (s *credentialsCtx) theContainerIsStopped() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) stagingDirNoLongerExists() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) threeContainersStartedAndStopped() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) noStagingDirsRemain() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) aDockerRuntimeWithUnwatchableCredentialsDir() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) containerStartSucceeds() error {
	return errors.New("pending: L2 hardening implementation")
}

func (s *credentialsCtx) warningLoggedWithContainerID() error {
	return errors.New("pending: L2 hardening implementation")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initCredentialsScenarios(ctx *godog.ScenarioContext) {
	s := &credentialsCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// L1: staging dir + watcher
	ctx.Step(`^a docker runtime configured with a credentials source directory$`, s.aDockerRuntimeWithCredentialsSourceDir)
	ctx.Step(`^a container is started$`, s.aContainerIsStarted)
	ctx.Step(`^a container has been started$`, s.aContainerHasBeenStarted)
	ctx.Step(`^the docker bind-mount argument references a directory not a single file$`, s.dockerBindMountArgReferencesDirectory)
	ctx.Step(`^the container path mounted is "([^"]*)"$`, s.containerPathMountedIs)
	ctx.Step(`^the host atomically replaces "([^"]*)" with new content$`, s.hostAtomicallyReplacesCredentials)
	ctx.Step(`^the staging directory reflects the new credentials content within (\d+) seconds$`, s.stagingDirReflectsNewContentWithinTimeout)
	ctx.Step(`^two containers are started concurrently$`, s.twoContainersStartedConcurrently)
	ctx.Step(`^each container has a distinct staging directory path$`, s.eachContainerHasDistinctStagingDir)

	// L2: hardening
	ctx.Step(`^the container is stopped$`, s.theContainerIsStopped)
	ctx.Step(`^the container's staging directory no longer exists on the host$`, s.stagingDirNoLongerExists)
	ctx.Step(`^three containers are started and then stopped$`, s.threeContainersStartedAndStopped)
	ctx.Step(`^no cloche credential staging directories remain on the host$`, s.noStagingDirsRemain)
	ctx.Step(`^a docker runtime where the credentials source directory cannot be watched$`, s.aDockerRuntimeWithUnwatchableCredentialsDir)
	ctx.Step(`^the container start succeeds$`, s.containerStartSucceeds)
	ctx.Step(`^a warning is logged that includes the container ID$`, s.warningLoggedWithContainerID)
}
