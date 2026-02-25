package ports

import (
	"context"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

type RunStore interface {
	CreateRun(ctx context.Context, run *domain.Run) error
	GetRun(ctx context.Context, id string) (*domain.Run, error)
	UpdateRun(ctx context.Context, run *domain.Run) error
	DeleteRun(ctx context.Context, id string) error
	ListRuns(ctx context.Context) ([]*domain.Run, error)
}

type CaptureStore interface {
	SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error
	GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error)
}

type EvolutionEntry struct {
	ID             string
	ProjectDir     string
	WorkflowName   string
	TriggerRunID   string
	CreatedAt      time.Time
	Classification string
	ChangesJSON    string
	KnowledgeDelta string
}

type EvolutionStore interface {
	SaveEvolution(ctx context.Context, entry *EvolutionEntry) error
	GetLastEvolution(ctx context.Context, projectDir, workflowName string) (*EvolutionEntry, error)
	ListRunsSince(ctx context.Context, projectDir, workflowName, sinceRunID string) ([]*domain.Run, error)
}
