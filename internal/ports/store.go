package ports

import (
	"context"
	"time"

	"github.com/swordsmanluke/cloche/internal/activitylog"
	"github.com/swordsmanluke/cloche/internal/domain"
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
	// ListRecentRunsByProject returns the most recent limit runs for a
	// project ordered strictly by start time (most recent first). Cheap
	// input for domain.CalculateHealth, unlike ListRunsByProject which
	// scans the project's entire run history.
	ListRecentRunsByProject(ctx context.Context, projectDir string, limit int) ([]*domain.Run, error)
	// CountActiveRunsByProject returns the number of pending/running runs
	// for a project. hostOnly restricts the count to host-orchestration
	// runs (used by the occupancy summary's no-loop-registered fallback).
	CountActiveRunsByProject(ctx context.Context, projectDir string, hostOnly bool) (int, error)
	ListRunsFiltered(ctx context.Context, filter domain.RunListFilter) ([]*domain.Run, error)
	// ListDoneRunsByProject returns top-level, terminal-state (succeeded/
	// failed/cancelled) runs for projectDir with a non-zero CompletedAt,
	// newest-completed first (ties broken deterministically, not merely by
	// natural table order, so a caller re-issuing the same query gets the
	// same order back). When before is non-zero, only runs completed at or
	// before it are considered — an inclusive bound, so a caller paging via
	// a (completed_at, task) cursor can still see every entry that shares
	// the cursor's exact completed_at and skip past its own entry itself.
	// limit bounds how many rows come back, so a page of the task-stack's
	// Done group can be fetched without scanning every run the project has
	// ever recorded.
	ListDoneRunsByProject(ctx context.Context, projectDir string, before time.Time, limit int) ([]*domain.Run, error)
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
	// ListContextKVForProject returns every context_kv row belonging to an
	// attempt of projectDir, for cross-attempt aggregation (the ledger
	// view's prompt-revision and requirement-injection tables).
	ListContextKVForProject(ctx context.Context, projectDir string) ([]ContextKVRow, error)
	// AttemptTokenTotals returns total (input+output) token usage per
	// attempt for projectDir, summed across every run tied to that attempt.
	AttemptTokenTotals(ctx context.Context, projectDir string) (map[string]int64, error)
}

// ContextKVRow is one row of the per-attempt context KV store, returned by
// ListContextKVForProject for cross-attempt aggregation.
type ContextKVRow struct {
	TaskID    string
	AttemptID string
	RunID     string
	Key       string
	Value     string
}

// ProjectMigrator is an optional interface that a RunStore may implement
// to perform per-project data migrations (e.g., moving log files to v2 layout).
type ProjectMigrator interface {
	MigrateProjectLogs(projectDir string) error
}

// LedgerBackfillStatus is an optional interface a RunStore may implement to
// report whether the one-time historical prompt-revision backfill (see
// internal/adapters/sqlite/ledger_backfill.go) has finished for a project.
// The ledger handler uses this to surface a backfill_pending flag instead of
// running the backfill itself on a request path.
type LedgerBackfillStatus interface {
	LedgerBackfillPending(ctx context.Context, projectDir string) (bool, error)
}

// RunHistoryProbe is an optional interface that a RunStore may implement to
// cheaply answer "is there any completed run further back than this point"
// without loading the runs themselves — used by the task-stack API to know
// whether to offer a cursor into older history without scanning it.
type RunHistoryProbe interface {
	HasCompletedRunBefore(ctx context.Context, projectDir string, before time.Time) (bool, error)
}

// BuiltinLookup is an optional interface that a RunStore may implement to
// batch the "is this task's runs a built-in workflow" check across many
// tasks in a single query, rather than one ListRunsFiltered call per task.
type BuiltinLookup interface {
	IsBuiltinByTaskIDs(ctx context.Context, taskIDs []string) (map[string]bool, error)
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
	// ListTasksByIDs loads exactly the given tasks (with attempts populated)
	// in a bounded query, for callers that already know which tasks they
	// need rather than a whole project's list.
	ListTasksByIDs(ctx context.Context, ids []string) ([]*domain.Task, error)
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
