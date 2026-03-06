package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// StoreRunWaiter polls the run store until a run reaches a terminal state.
type StoreRunWaiter struct {
	Store ports.RunStore
}

// WaitRun polls GetRun every 2 seconds until the run reaches a terminal state.
func (w *StoreRunWaiter) WaitRun(ctx context.Context, runID string) (domain.RunState, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		run, err := w.Store.GetRun(ctx, runID)
		if err != nil {
			return "", fmt.Errorf("getting run %s: %w", runID, err)
		}

		switch run.State {
		case domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled:
			return run.State, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
