package ports

import (
	"context"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/domain"
)

// UsageQuery holds filter parameters for token usage aggregation queries.
type UsageQuery struct {
	ProjectDir string    // empty = all projects
	AgentName  string    // empty = all agents
	Since      time.Time // zero = no lower bound
	Until      time.Time // zero = no upper bound
}

type RunStore interface {
	CreateRun(ctx context.Context, run *domain.Run) error
	GetRun(ctx context.Context, id string) (*domain.Run, error)
	GetRunByAttempt(ctx context.Context, attemptID, id string) (*domain.Run, error)
	UpdateRun(ctx context.Context, run *domain.Run) error
	DeleteRun(ctx context.Context, id string) error
	ListRuns(ctx context.Context, since time.Time) ([]*domain.Run, error)
	ListRunsByProject(ctx context.Context, projectDir string, since time.Time) ([]*domain.Run, error)
	ListRunsFiltered(ctx context.Context, filter domain.RunListFilter) ([]*domain.Run, error)
	ListProjects(ctx context.Context) ([]string, error)
	ListChildRuns(ctx context.Context, parentRunID string) ([]*domain.Run, error)
	QueryUsage(ctx context.Context, q UsageQuery) ([]domain.UsageSummary, error)
	GetContextKey(ctx context.Context, taskID, attemptID, runID, key string) (string, bool, error)
	SetContextKey(ctx context.Context, taskID, attemptID, runID, key, value string) error
	ListContextKeys(ctx context.Context, taskID, attemptID, runID string) ([]string, error)
	DeleteContextKeys(ctx context.Context, taskID, attemptID string) error
}

// ProjectMigrator is an optional interface that a RunStore may implement
// to perform per-project data migrations (e.g., moving log files to v2 layout).
type ProjectMigrator interface {
	MigrateProjectLogs(projectDir string) error
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
	SaveAttemptLog(ctx context.Context, entry *AttemptLogEntry) error
	GetAttemptLogs(ctx context.Context, attemptID string) ([]*AttemptLogEntry, error)
}

type TaskStore interface {
	SaveTask(ctx context.Context, task *domain.Task) error
	GetTask(ctx context.Context, id string) (*domain.Task, error)
	ListTasks(ctx context.Context, projectDir string) ([]*domain.Task, error)
}

type AttemptStore interface {
	SaveAttempt(ctx context.Context, attempt *domain.Attempt) error
	GetAttempt(ctx context.Context, id string) (*domain.Attempt, error)
	ListAttempts(ctx context.Context, taskID string) ([]*domain.Attempt, error)
	FailStaleAttempts(ctx context.Context) (int64, error)
}

type AttemptLogEntry struct {
	ID        int64
	AttemptID string
	TaskID    string
	FileType  string // "full", "script", "llm"
	FilePath  string
	FileSize  int64
	CreatedAt time.Time
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

// ActivityStore persists and retrieves project activity log entries.
type ActivityStore interface {
	AppendActivityEntry(ctx context.Context, projectDir string, entry activitylog.Entry) error
	ReadActivityEntries(ctx context.Context, projectDir string, opts activitylog.ReadOptions) ([]activitylog.Entry, error)
}

type EvolutionStore interface {
	SaveEvolution(ctx context.Context, entry *EvolutionEntry) error
	GetLastEvolution(ctx context.Context, projectDir, workflowName string) (*EvolutionEntry, error)
	ListRunsSince(ctx context.Context, projectDir, workflowName, sinceRunID string) ([]*domain.Run, error)
}
