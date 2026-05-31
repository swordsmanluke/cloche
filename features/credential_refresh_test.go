package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cucumber/godog"
)

// credRefreshCtx holds per-scenario state for the credential refresh scenarios.
type credRefreshCtx struct {
	claudeDir    string // fake ~/.claude/ for this scenario
	containerDir string // fake container-side credential dir for this scenario
	refresher    *docker.CredentialRefresher
	initialContent   string
	refreshedContent string
}

func (s *credRefreshCtx) reset() {
	if s.refresher != nil {
		s.refresher.Close()
		s.refresher = nil
	}
	if s.claudeDir != "" {
		os.RemoveAll(s.claudeDir)
		s.claudeDir = ""
	}
	if s.containerDir != "" {
		os.RemoveAll(s.containerDir)
		s.containerDir = ""
	}
	s.initialContent = ""
	s.refreshedContent = ""
}

// ─── background ──────────────────────────────────────────────────────────────

func (s *credRefreshCtx) theClocheDAemonIsRunning() error {
	// Unit-level BDD test: no real daemon needed. Formal integration tests
	// that exercise a live daemon are deferred to L2.
	return nil
}

// ─── Scenario 1: authentication on container startup ─────────────────────────

func (s *credRefreshCtx) theHostHasAValidCredentialsFile() error {
	dir, err := os.MkdirTemp("", "cloche-bdd-claude-*")
	if err != nil {
		return fmt.Errorf("creating fake claude dir: %w", err)
	}
	s.claudeDir = dir
	s.initialContent = `{"token":"initial-token","expiresAt":"2099-01-01T00:00:00Z"}`
	return os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(s.initialContent), 0644)
}

func (s *credRefreshCtx) aClocheAttemptIsStarted() error {
	// Simulate the container-side directory where credentials would be docker-cp'd.
	dir, err := os.MkdirTemp("", "cloche-bdd-container-*")
	if err != nil {
		return fmt.Errorf("creating fake container dir: %w", err)
	}
	s.containerDir = dir

	// Copy initial credentials (simulates the docker-cp at container start).
	src := filepath.Join(s.claudeDir, ".credentials.json")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading initial credentials: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.containerDir, ".credentials.json"), data, 0644); err != nil {
		return fmt.Errorf("writing initial credentials to container dir: %w", err)
	}

	// Start the watcher with a local-copy function instead of docker-cp, so
	// the test runs without Docker. The local-copy function writes the file
	// directly to s.containerDir (simulating what docker cp would do).
	containerDir := s.containerDir
	copyFn := func(src, containerID, destPath string) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dest := filepath.Join(containerDir, filepath.Base(destPath))
		return os.WriteFile(dest, data, 0644)
	}

	s.refresher = docker.NewCredentialRefresherWithCopy("bdd-test-container", s.claudeDir, s.containerDir+"/", copyFn)
	return nil
}

func (s *credRefreshCtx) anAgentStepExecutes() error {
	// The agent step reads credentials from the container dir (simulating the
	// container's /home/agent/.claude/.credentials.json).
	_, err := os.ReadFile(filepath.Join(s.containerDir, ".credentials.json"))
	if err != nil {
		return fmt.Errorf("agent cannot read credentials from container dir: %w", err)
	}
	return nil
}

func (s *credRefreshCtx) stepCompletesWithoutAuthError() error {
	expected := s.refreshedContent
	if expected == "" {
		expected = s.initialContent
	}
	data, err := os.ReadFile(filepath.Join(s.containerDir, ".credentials.json"))
	if err != nil {
		return fmt.Errorf("reading credentials from container dir: %w", err)
	}
	if string(data) != expected {
		return fmt.Errorf("credentials mismatch: got %q, want %q", string(data), expected)
	}
	return nil
}

// ─── Scenario 2: re-authentication after atomic host rename ──────────────────

func (s *credRefreshCtx) attemptInProgressWithCompletedStep() error {
	if err := s.theHostHasAValidCredentialsFile(); err != nil {
		return err
	}
	if err := s.aClocheAttemptIsStarted(); err != nil {
		return err
	}
	return s.anAgentStepExecutes()
}

func (s *credRefreshCtx) hostAtomicallyReplacesCreds() error {
	s.refreshedContent = `{"token":"refreshed-token","expiresAt":"2099-06-01T00:00:00Z"}`

	// Write new credentials to a temp file, then atomically rename it — the
	// same pattern the OAuth client uses.
	tmpFile := filepath.Join(s.claudeDir, ".credentials.json.tmp")
	if err := os.WriteFile(tmpFile, []byte(s.refreshedContent), 0644); err != nil {
		return fmt.Errorf("writing temp credentials: %w", err)
	}
	return os.Rename(tmpFile, filepath.Join(s.claudeDir, ".credentials.json"))
}

func (s *credRefreshCtx) anotherAgentStepExecutes() error {
	// Poll until the watcher has re-copied the refreshed credentials into the
	// container dir, or until a 3-second deadline is reached.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(s.containerDir, ".credentials.json"))
		if err == nil && string(data) == s.refreshedContent {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(filepath.Join(s.containerDir, ".credentials.json"))
	return fmt.Errorf("container dir not updated within 3s: got %q, want %q", string(data), s.refreshedContent)
}

// ─── Scenario 3 & 4: L2 — pending ───────────────────────────────────────────

func (s *credRefreshCtx) attemptFinishedContainerStopped() error {
	return godog.ErrPending
}

func (s *credRefreshCtx) noStagingDirsRemain() error {
	return godog.ErrPending
}

func (s *credRefreshCtx) credsDirNotWatchable() error {
	return godog.ErrPending
}

func (s *credRefreshCtx) daemonStartsContainer() error {
	return godog.ErrPending
}

func (s *credRefreshCtx) warningIdentifiesContainerID() error {
	return godog.ErrPending
}

func (s *credRefreshCtx) noSilentFallbackToSingleFile() error {
	return godog.ErrPending
}

// ─── scenario initializer ────────────────────────────────────────────────────

func initCredentialRefreshScenarios(ctx *godog.ScenarioContext) {
	s := &credRefreshCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})
	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^the cloche daemon is running$`, s.theClocheDAemonIsRunning)

	// Scenario 1: startup
	ctx.Step(`^the host has a valid credentials file$`, s.theHostHasAValidCredentialsFile)
	ctx.Step(`^a cloche attempt is started for a project$`, s.aClocheAttemptIsStarted)
	ctx.Step(`^an agent step executes inside the container$`, s.anAgentStepExecutes)
	ctx.Step(`^the step completes without an authentication error$`, s.stepCompletesWithoutAuthError)

	// Scenario 2: re-auth after atomic rename
	ctx.Step(`^a cloche attempt is in progress with at least one completed agent step$`, s.attemptInProgressWithCompletedStep)
	ctx.Step(`^the host atomically replaces its credentials file via rename$`, s.hostAtomicallyReplacesCreds)
	ctx.Step(`^another agent step executes inside the same container$`, s.anotherAgentStepExecutes)

	// Scenario 3: staging directory cleanup (L2)
	ctx.Step(`^a cloche attempt has finished and the container has been stopped$`, s.attemptFinishedContainerStopped)
	ctx.Step(`^no cloche credential staging directories remain on the host$`, s.noStagingDirsRemain)

	// Scenario 4: watcher failure visibility (L2)
	ctx.Step(`^the host credentials directory cannot be watched by fsnotify$`, s.credsDirNotWatchable)
	ctx.Step(`^the daemon starts a container for a new attempt$`, s.daemonStartsContainer)
	ctx.Step(`^a warning log entry identifies the affected container by ID$`, s.warningIdentifiesContainerID)
	ctx.Step(`^the daemon does not silently fall back to the old single-file bind-mount$`, s.noSilentFallbackToSingleFile)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initCredentialRefreshScenarios,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"credential_refresh.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
