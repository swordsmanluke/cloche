package grpc

import (
	"context"
	"log"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// resolvedRepo describes one repository the daemon should extract changes into
// for a given workflow run. Alias of config.ResolvedRepo, the shared
// repo-resolution type also used by the intent-scan collector.
type resolvedRepo = config.ResolvedRepo

// attemptRepoSelection resolves the repos the current attempt's containers hold
// and extract to. An attempt that selected none gets every configured repo.
func (d *DaemonExecutor) attemptRepoSelection(ctx context.Context) (*config.RepoSelection, error) {
	cfg, err := config.Load(d.projectDir)
	if err != nil {
		log.Printf("daemon executor: config.Load(%s): %v — treating as no [[repositories]] declared", d.projectDir, err)
		cfg = nil
	}
	return cfg.SelectRepositories(d.projectDir, attemptRepos(ctx, d.store, d.attemptID))
}

// attemptRepos returns the repos attemptID selected, or nil when it selected
// none or has no record.
func attemptRepos(ctx context.Context, store ports.RunStore, attemptID string) []string {
	if store == nil || attemptID == "" {
		return nil
	}
	attempt, err := store.GetAttempt(ctx, attemptID)
	if err != nil || attempt == nil {
		return nil
	}
	return attempt.Repos
}
