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
	SaveAttempt(ctx context.Context, attempt *domain.Attempt) error
	GetAttempt(ctx context.Context, id string) (*domain.Attempt, error)
	ListAttempts(ctx context.Context, taskID string) ([]*domain.Attempt, error)
	FailStaleAttempts(ctx context.Context) (int64, error)
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

type AttemptLogEntry struct {
	ID        int64
	AttemptID string
	TaskID    string
	FileType  string // "full", "script", "llm"
	FilePath  string
	FileSize  int64
	CreatedAt time.Time
}

// ActivityStore persists and retrieves project activity log entries.
type ActivityStore interface {
	AppendActivityEntry(ctx context.Context, projectDir string, entry activitylog.Entry) error
	ReadActivityEntries(ctx context.Context, projectDir string, opts activitylog.ReadOptions) ([]activitylog.Entry, error)
}

// PollRecord tracks the polling state of a poll step within a run.
type PollRecord struct {
	RunID      string
	StepName   string
	StartedAt  time.Time
	LastPollAt time.Time
	PollCount  int
}

// PollStore persists and retrieves poll step poll state.
// This enables the daemon to surface "waiting" status and survive restarts
// while a poll step is being polled.
type PollStore interface {
	UpsertPoll(ctx context.Context, record *PollRecord) error
	GetPoll(ctx context.Context, runID, stepName string) (*PollRecord, error)
	DeletePoll(ctx context.Context, runID, stepName string) error
	ListPolls(ctx context.Context, runID string) ([]*PollRecord, error)
}

// HelpThreadFilter holds optional filters for listing help threads.
type HelpThreadFilter struct {
	Channel string // empty = all channels
	All     bool   // include closed/archived threads; false = only awaiting_user/awaiting_agent
}

// HelpStore persists help threads and messages (see internal/domain/help.go).
// Follows the PollStore pattern.
type HelpStore interface {
	CreateThread(ctx context.Context, thread *domain.HelpThread) error
	AppendMessage(ctx context.Context, msg *domain.HelpMessage) error
	GetThread(ctx context.Context, threadID string) (*domain.HelpThread, []domain.HelpMessage, error)
	// ResolveThread accepts a "<channel>/<name>" address, a bare thread ID, or
	// (when unambiguous) a bare name, and returns the matching thread.
	ResolveThread(ctx context.Context, address string) (*domain.HelpThread, error)
	ListThreads(ctx context.Context, filter HelpThreadFilter) ([]*domain.HelpThread, error)
	// ListOpenThreadsByRun returns threads awaiting a user reply for the given run.
	// Under the "one open ask per run" rule this is 0 or 1 threads in practice.
	ListOpenThreadsByRun(ctx context.Context, runID string) ([]*domain.HelpThread, error)
	SetThreadState(ctx context.Context, threadID string, state domain.ThreadState) error
	// CountThreadsWithNamePrefix returns how many threads in a channel already
	// have a name starting with prefix, used to allocate the next name suffix.
	CountThreadsWithNamePrefix(ctx context.Context, channel, prefix string) (int, error)
	// ArchiveThreadsByTask moves all of a task's non-archived threads to the
	// archived state. Called when the owning task completes successfully.
	ArchiveThreadsByTask(ctx context.Context, taskID string) (int64, error)
	// DeleteArchivedThreadsOlderThan deletes archived threads (and their
	// messages/bindings) whose ArchivedAt is before cutoff.
	DeleteArchivedThreadsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	// Channel bindings: map external ids (e.g. Slack thread_ts) to thread ids.
	BindExternal(ctx context.Context, threadID, channelName, externalID string) error
	ResolveExternal(ctx context.Context, channelName, externalID string) (threadID string, err error)
	// GetExternalID is the reverse lookup of ResolveExternal: given an internal
	// thread ID, returns the external id bound for that channel, or "" if unbound.
	GetExternalID(ctx context.Context, threadID, channelName string) (externalID string, err error)
	// FindAskAnswer looks up an ask on this run identified by askKey (see
	// HelpMessage.AskKey) that has since received a user reply, and returns
	// that reply. Used for idempotent replay: a step re-running from scratch
	// after a park+resume re-issues the same ask and gets the answer instantly
	// instead of re-blocking or re-posting to channels.
	FindAskAnswer(ctx context.Context, runID, askKey string) (answer, threadID string, found bool, err error)
}
