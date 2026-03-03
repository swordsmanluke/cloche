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
	ListRuns(ctx context.Context, since time.Time) ([]*domain.Run, error)
	ListRunsByProject(ctx context.Context, projectDir string, since time.Time) ([]*domain.Run, error)
	ListProjects(ctx context.Context) ([]string, error)
}

type CaptureStore interface {
	SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error
	GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error)
}

type LogFileEntry struct {
	ID        int64
	RunID     string
	StepName  string
	FileType  string // "full", "script", "llm"
	FilePath  string
	FileSize  int64
	CreatedAt time.Time
}

type LogStore interface {
	SaveLogFile(ctx context.Context, entry *LogFileEntry) error
	GetLogFiles(ctx context.Context, runID string) ([]*LogFileEntry, error)
	GetLogFilesByStep(ctx context.Context, runID, stepName string) ([]*LogFileEntry, error)
	GetLogFileByType(ctx context.Context, runID, fileType string) ([]*LogFileEntry, error)
}

type MergeQueueEntry struct {
	RunID       string
	Branch      string
	Project     string
	Status      string // "pending", "in_progress", "completed", "failed"
	EnqueuedAt  time.Time
	CompletedAt time.Time
}

type MergeQueueStore interface {
	EnqueueMerge(ctx context.Context, entry *MergeQueueEntry) error
	NextPendingMerge(ctx context.Context, project string) (*MergeQueueEntry, error)
	CompleteMerge(ctx context.Context, runID string) error
	FailMerge(ctx context.Context, runID string) error
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
