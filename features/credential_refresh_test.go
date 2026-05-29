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

// credentialCtx holds per-scenario state for credential refresh tests.
type credentialCtx struct {
	claudeDir      string // temp dir acting as the host ~/.claude/
	stagingDir     string
	watcher        interface{ Close() error }
	initialCreds   string
	refreshedCreds string
	lastReadCreds  string
}

func (c *credentialCtx) reset() {
	if c.watcher != nil {
		c.watcher.Close()
	}
	if c.stagingDir != "" {
		os.RemoveAll(c.stagingDir)
	}
	if c.claudeDir != "" {
		os.RemoveAll(c.claudeDir)
	}
	*c = credentialCtx{}
}

func (c *credentialCtx) setup() error {
	dir, err := os.MkdirTemp("", "cloche-bdd-claude-")
	if err != nil {
		return fmt.Errorf("creating temp claude dir: %w", err)
	}
	c.claudeDir = dir
	return nil
}

// ─── Background ──────────────────────────────────────────────────────────────

func (c *credentialCtx) theClocheDAemonIsRunning() error {
	return c.setup()
}

// ─── Scenario 1: Authentication on startup ───────────────────────────────────

func (c *credentialCtx) theHostHasAValidCredentialsFile() error {
	c.initialCreds = `{"access_token":"initial-token","token_type":"Bearer"}`
	return os.WriteFile(
		filepath.Join(c.claudeDir, ".credentials.json"),
		[]byte(c.initialCreds),
		0644,
	)
}

func (c *credentialCtx) aClocheAttemptIsStarted() error {
	sd, err := docker.CreateCredentialStagingDir(c.claudeDir)
	if err != nil {
		return fmt.Errorf("creating credential staging dir: %w", err)
	}
	c.stagingDir = sd
	return nil
}

func (c *credentialCtx) anAgentStepExecutes() error {
	data, err := os.ReadFile(filepath.Join(c.stagingDir, ".credentials.json"))
	if err != nil {
		return fmt.Errorf("agent step: reading credentials from staging dir: %w", err)
	}
	c.lastReadCreds = string(data)
	return nil
}

func (c *credentialCtx) stepCompletesWithoutAuthError() error {
	if c.lastReadCreds == "" {
		return fmt.Errorf("no credentials were read by the agent step")
	}
	expected := c.refreshedCreds
	if expected == "" {
		expected = c.initialCreds
	}
	if c.lastReadCreds != expected {
		return fmt.Errorf("credentials mismatch: want %q, got %q", expected, c.lastReadCreds)
	}
	return nil
}

// ─── Scenario 2: Re-authentication after atomic rename ───────────────────────

func (c *credentialCtx) attemptInProgressWithCompletedStep() error {
	// Start the staging dir and watcher to simulate an in-progress attempt.
	if err := c.aClocheAttemptIsStarted(); err != nil {
		return err
	}
	w, err := docker.StartCredentialWatcher(c.stagingDir, c.claudeDir, "test-container-id")
	if err != nil {
		return fmt.Errorf("starting credential watcher: %w", err)
	}
	c.watcher = w
	// Simulate a completed step: verify initial credentials are readable.
	return c.anAgentStepExecutes()
}

func (c *credentialCtx) hostAtomicallyReplacesCreds() error {
	c.refreshedCreds = `{"access_token":"refreshed-token","token_type":"Bearer"}`
	// Write to a temp file then rename (atomic replace, new inode).
	tmpPath := filepath.Join(c.claudeDir, ".credentials.json.tmp")
	if err := os.WriteFile(tmpPath, []byte(c.refreshedCreds), 0644); err != nil {
		return fmt.Errorf("writing temp credentials: %w", err)
	}
	return os.Rename(tmpPath, filepath.Join(c.claudeDir, ".credentials.json"))
}

func (c *credentialCtx) anotherAgentStepExecutes() error {
	// Poll the staging dir until the watcher has propagated the update.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(c.stagingDir, ".credentials.json"))
		if err == nil && string(data) == c.refreshedCreds {
			c.lastReadCreds = string(data)
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	data, _ := os.ReadFile(filepath.Join(c.stagingDir, ".credentials.json"))
	return fmt.Errorf("staging dir not updated within timeout; got %q, want %q", string(data), c.refreshedCreds)
}

// ─── Scenario 3 & 4: L2 (pending) ───────────────────────────────────────────

func (c *credentialCtx) attemptFinishedContainerStopped() error {
	return godog.ErrPending
}

func (c *credentialCtx) noStagingDirsRemain() error {
	return godog.ErrPending
}

func (c *credentialCtx) credsDirNotWatchable() error {
	return godog.ErrPending
}

func (c *credentialCtx) daemonStartsContainer() error {
	return godog.ErrPending
}

func (c *credentialCtx) warningIdentifiesContainerID() error {
	return godog.ErrPending
}

func (c *credentialCtx) noSilentFallbackToSingleFile() error {
	return godog.ErrPending
}

// ─── Step registration ───────────────────────────────────────────────────────

func initCredentialRefreshScenarios(ctx *godog.ScenarioContext) {
	c := &credentialCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		c.reset()
		return nil, nil
	})

	// Background
	ctx.Step(`^the cloche daemon is running$`, c.theClocheDAemonIsRunning)

	// Scenario 1
	ctx.Step(`^the host has a valid credentials file$`, c.theHostHasAValidCredentialsFile)
	ctx.Step(`^a cloche attempt is started for a project$`, c.aClocheAttemptIsStarted)
	ctx.Step(`^an agent step executes inside the container$`, c.anAgentStepExecutes)
	ctx.Step(`^the step completes without an authentication error$`, c.stepCompletesWithoutAuthError)

	// Scenario 2
	ctx.Step(`^a cloche attempt is in progress with at least one completed agent step$`, c.attemptInProgressWithCompletedStep)
	ctx.Step(`^the host atomically replaces its credentials file via rename$`, c.hostAtomicallyReplacesCreds)
	ctx.Step(`^another agent step executes inside the same container$`, c.anotherAgentStepExecutes)

	// Scenario 3 (L2)
	ctx.Step(`^a cloche attempt has finished and the container has been stopped$`, c.attemptFinishedContainerStopped)
	ctx.Step(`^no cloche credential staging directories remain on the host$`, c.noStagingDirsRemain)

	// Scenario 4 (L2)
	ctx.Step(`^the host credentials directory cannot be watched by fsnotify$`, c.credsDirNotWatchable)
	ctx.Step(`^the daemon starts a container for a new attempt$`, c.daemonStartsContainer)
	ctx.Step(`^a warning log entry identifies the affected container by ID$`, c.warningIdentifiesContainerID)
	ctx.Step(`^the daemon does not silently fall back to the old single-file bind-mount$`, c.noSilentFallbackToSingleFile)
}

// ─── Test entry point ────────────────────────────────────────────────────────

func TestCredentialRefresh(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initCredentialRefreshScenarios,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"credential_refresh.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run credential refresh feature tests")
	}
}
