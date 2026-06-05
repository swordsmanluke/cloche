package features_test

import (
	"context"
	"errors"
	"os"

	"github.com/cucumber/godog"
)

// credentialsMountCtx holds per-scenario state for credentials-mount BDD scenarios.
type credentialsMountCtx struct {
	claudeHomeDir string // temp dir used as the host Claude home
	stagingDir    string // staging dir created by the runtime; inspected in L2 cleanup tests
}

func (s *credentialsMountCtx) reset() {
	if s.claudeHomeDir != "" {
		os.RemoveAll(s.claudeHomeDir)
	}
	*s = credentialsMountCtx{}
}

// ─── Background ──────────────────────────────────────────────────────────────

func (s *credentialsMountCtx) aTempDirAsClaudeHome() error {
	dir, err := os.MkdirTemp("", "cloche-claude-home-*")
	if err != nil {
		return err
	}
	s.claudeHomeDir = dir
	return nil
}

// ─── L1: core fix steps ──────────────────────────────────────────────────────

func (s *credentialsMountCtx) credentialsFileExists() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) noCredentialsFile() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) runtimePreparesStartArgs() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) runtimeStartedWithStagedCreds() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) credentialsReplacedAtomically() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) stagedCopyHasNewContent() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) volumeArgMountsStagingDir() error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) destinationInsideContainer(_ string) error {
	return errors.New("pending: L1 implementation")
}

func (s *credentialsMountCtx) noVolumeArgReferencesCredentialsFile() error {
	return errors.New("pending: L1 implementation")
}

// ─── L2: hardening steps ─────────────────────────────────────────────────────

func (s *credentialsMountCtx) containerIsStoppedViaRuntime() error {
	return errors.New("pending: L2 implementation")
}

func (s *credentialsMountCtx) stagingDirNoLongerExists() error {
	return errors.New("pending: L2 implementation")
}

func (s *credentialsMountCtx) credentialsCopyIsBlocked() error {
	return errors.New("pending: L2 implementation")
}

func (s *credentialsMountCtx) runtimeContinuesWithoutError() error {
	return errors.New("pending: L2 implementation")
}

func (s *credentialsMountCtx) warningIsEmitted() error {
	return errors.New("pending: L2 implementation")
}

// ─── Step registration ────────────────────────────────────────────────────────

func initCredentialsMountScenarios(ctx *godog.ScenarioContext) {
	s := &credentialsMountCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^a temporary directory is used as the host Claude home$`, s.aTempDirAsClaudeHome)

	// L1: core fix
	ctx.Step(`^a credentials file exists in the host Claude home$`, s.credentialsFileExists)
	ctx.Step(`^no credentials file exists in the host Claude home$`, s.noCredentialsFile)
	ctx.Step(`^the docker runtime prepares start arguments for a new container$`, s.runtimePreparesStartArgs)
	ctx.Step(`^the docker runtime has started a container with staged credentials$`, s.runtimeStartedWithStagedCreds)
	ctx.Step(`^the credentials file on the host is replaced via atomic rename$`, s.credentialsReplacedAtomically)
	ctx.Step(`^the staged copy contains the new credentials content$`, s.stagedCopyHasNewContent)
	ctx.Step(`^the volume arg mounts the staging directory, not the credentials file path$`, s.volumeArgMountsStagingDir)
	ctx.Step(`^the destination inside the container is "([^"]*)"$`, s.destinationInsideContainer)
	ctx.Step(`^no volume arg references the credentials file$`, s.noVolumeArgReferencesCredentialsFile)

	// L2: hardening
	ctx.Step(`^the container is stopped via the docker runtime$`, s.containerIsStoppedViaRuntime)
	ctx.Step(`^the staging directory no longer exists on disk$`, s.stagingDirNoLongerExists)
	ctx.Step(`^the credentials file changes but writing to the staging directory is blocked$`, s.credentialsCopyIsBlocked)
	ctx.Step(`^the runtime continues running without returning an error$`, s.runtimeContinuesWithoutError)
	ctx.Step(`^a warning is emitted to the log$`, s.warningIsEmitted)
}
