package ports

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

type RunStore interface {
	CreateRun(ctx context.Context, run *domain.Run) error
	GetRun(ctx context.Context, id string) (*domain.Run, error)
	UpdateRun(ctx context.Context, run *domain.Run) error
	ListRuns(ctx context.Context) ([]*domain.Run, error)
}

type CaptureStore interface {
	SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error
	GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error)
}
