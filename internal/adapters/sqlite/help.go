package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

const helpThreadSelectColumns = `SELECT id, channel, name, task_id, attempt_id, run_id, step_name, title, state, created_at, updated_at, archived_at`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanHelpThread(row rowScanner) (*domain.HelpThread, error) {
	t := &domain.HelpThread{}
	var state, createdAt, updatedAt string
	var archivedAt sql.NullString
	if err := row.Scan(&t.ID, &t.Channel, &t.Name, &t.TaskID, &t.AttemptID, &t.RunID, &t.StepName,
		&t.Title, &state, &createdAt, &updatedAt, &archivedAt); err != nil {
		return nil, err
	}
	t.State = domain.ThreadState(state)
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	if archivedAt.Valid && archivedAt.String != "" {
		ts := parseTime(archivedAt.String)
		t.ArchivedAt = &ts
	}
	return t, nil
}

func nullableTimePtr(t *time.Time) interface{} {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

// CreateThread inserts a new help thread.
func (s *Store) CreateThread(ctx context.Context, thread *domain.HelpThread) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO help_threads (id, channel, name, task_id, attempt_id, run_id, step_name, title, state, created_at, updated_at, archived_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		thread.ID, thread.Channel, thread.Name, thread.TaskID, thread.AttemptID, thread.RunID, thread.StepName,
		thread.Title, string(thread.State), formatTime(thread.CreatedAt), formatTime(thread.UpdatedAt),
		nullableTimePtr(thread.ArchivedAt),
	)
	return err
}

// AppendMessage inserts a new message and bumps the thread's updated_at.
func (s *Store) AppendMessage(ctx context.Context, msg *domain.HelpMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO help_messages (id, thread_id, author, body, options, ask_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.ThreadID, string(msg.Author), msg.Body, strings.Join(msg.Options, ","), msg.AskKey, formatTime(msg.CreatedAt),
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE help_threads SET updated_at = ? WHERE id = ?`, formatTime(time.Now()), msg.ThreadID)
	return err
}

func (s *Store) scanThreadByID(ctx context.Context, id string) (*domain.HelpThread, error) {
	row := s.db.QueryRowContext(ctx, helpThreadSelectColumns+` FROM help_threads WHERE id = ?`, id)
	t, err := scanHelpThread(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) listMessages(ctx context.Context, threadID string) ([]domain.HelpMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, thread_id, author, body, options, ask_key, created_at FROM help_messages WHERE thread_id = ? ORDER BY created_at ASC, id ASC`,
		threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []domain.HelpMessage
	for rows.Next() {
		var m domain.HelpMessage
		var author, options, createdAt string
		if err := rows.Scan(&m.ID, &m.ThreadID, &author, &m.Body, &options, &m.AskKey, &createdAt); err != nil {
			return nil, err
		}
		m.Author = domain.MessageAuthor(author)
		if options != "" {
			m.Options = strings.Split(options, ",")
		}
		m.CreatedAt = parseTime(createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetThread returns a thread and its full transcript by internal ID.
func (s *Store) GetThread(ctx context.Context, threadID string) (*domain.HelpThread, []domain.HelpMessage, error) {
	t, err := s.scanThreadByID(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if t == nil {
		return nil, nil, fmt.Errorf("help thread not found: %s", threadID)
	}
	msgs, err := s.listMessages(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	return t, msgs, nil
}

// ResolveThread accepts a "<channel>/<name>" address, a bare thread ID
// ("thread-..."), or (when unambiguous) a bare name.
func (s *Store) ResolveThread(ctx context.Context, address string) (*domain.HelpThread, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, fmt.Errorf("empty thread address")
	}

	if channel, name, ok := strings.Cut(address, "/"); ok {
		row := s.db.QueryRowContext(ctx, helpThreadSelectColumns+` FROM help_threads WHERE channel = ? AND name = ?`, channel, name)
		t, err := scanHelpThread(row)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no help thread found matching %q", address)
		}
		return t, err
	}

	if strings.HasPrefix(address, "thread-") {
		if t, err := s.scanThreadByID(ctx, address); err != nil {
			return nil, err
		} else if t != nil {
			return t, nil
		}
	}

	rows, err := s.db.QueryContext(ctx, helpThreadSelectColumns+` FROM help_threads WHERE name = ?`, address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*domain.HelpThread
	for rows.Next() {
		t, err := scanHelpThread(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no help thread found matching %q", address)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("thread name %q is ambiguous across channels; use <channel>/%s", address, address)
	}
}

// ListThreads lists threads, optionally filtered to a channel and/or
// restricted to open (awaiting_user/awaiting_agent) threads only.
func (s *Store) ListThreads(ctx context.Context, filter ports.HelpThreadFilter) ([]*domain.HelpThread, error) {
	query := helpThreadSelectColumns + ` FROM help_threads WHERE 1=1`
	var args []interface{}
	if filter.Channel != "" {
		query += ` AND channel = ?`
		args = append(args, filter.Channel)
	}
	if !filter.All {
		query += ` AND state IN (?, ?)`
		args = append(args, string(domain.ThreadStateAwaitingUser), string(domain.ThreadStateAwaitingAgent))
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []*domain.HelpThread
	for rows.Next() {
		t, err := scanHelpThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

// ListOpenThreadsByRun returns threads awaiting a user reply for the given run.
func (s *Store) ListOpenThreadsByRun(ctx context.Context, runID string) ([]*domain.HelpThread, error) {
	rows, err := s.db.QueryContext(ctx,
		helpThreadSelectColumns+` FROM help_threads WHERE run_id = ? AND state = ? ORDER BY created_at DESC`,
		runID, string(domain.ThreadStateAwaitingUser))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []*domain.HelpThread
	for rows.Next() {
		t, err := scanHelpThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

// SetThreadState updates a thread's state and bumps updated_at.
func (s *Store) SetThreadState(ctx context.Context, threadID string, state domain.ThreadState) error {
	_, err := s.db.ExecContext(ctx, `UPDATE help_threads SET state = ?, updated_at = ? WHERE id = ?`,
		string(state), formatTime(time.Now()), threadID)
	return err
}

// CountThreadsWithNamePrefix returns how many threads in a channel already
// have this exact name or a name starting with "prefix-", used to allocate
// the next name suffix ("schema-choice-7").
func (s *Store) CountThreadsWithNamePrefix(ctx context.Context, channel, prefix string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM help_threads WHERE channel = ? AND (name = ? OR name LIKE ?)`,
		channel, prefix, prefix+"-%").Scan(&count)
	return count, err
}

// ArchiveThreadsByTask moves all of a task's non-archived threads to the
// archived state. Called when the owning task completes successfully.
func (s *Store) ArchiveThreadsByTask(ctx context.Context, taskID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE help_threads SET state = ?, archived_at = ?, updated_at = ? WHERE task_id = ? AND state != ?`,
		string(domain.ThreadStateArchived), formatTime(time.Now()), formatTime(time.Now()), taskID, string(domain.ThreadStateArchived))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteArchivedThreadsOlderThan deletes archived threads (and their
// messages/bindings) whose ArchivedAt is before cutoff.
func (s *Store) DeleteArchivedThreadsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM help_threads WHERE state = ? AND archived_at IS NOT NULL AND archived_at < ?`,
		string(domain.ThreadStateArchived), formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var deleted int64
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM help_messages WHERE thread_id = ?`, id); err != nil {
			return deleted, err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM help_bindings WHERE thread_id = ?`, id); err != nil {
			return deleted, err
		}
		res, err := s.db.ExecContext(ctx, `DELETE FROM help_threads WHERE id = ?`, id)
		if err != nil {
			return deleted, err
		}
		if n, err := res.RowsAffected(); err == nil {
			deleted += n
		}
	}
	return deleted, nil
}

// BindExternal maps an external channel id (e.g. Slack thread_ts) to a thread.
func (s *Store) BindExternal(ctx context.Context, threadID, channelName, externalID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO help_bindings (thread_id, channel_name, external_id) VALUES (?, ?, ?)
		 ON CONFLICT(channel_name, external_id) DO UPDATE SET thread_id = excluded.thread_id`,
		threadID, channelName, externalID)
	return err
}

// ResolveExternal looks up a thread ID by external channel id. Returns "" if unbound.
func (s *Store) ResolveExternal(ctx context.Context, channelName, externalID string) (string, error) {
	var threadID string
	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id FROM help_bindings WHERE channel_name = ? AND external_id = ?`,
		channelName, externalID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return threadID, err
}

// GetExternalID is the reverse lookup of ResolveExternal: given an internal
// thread ID, returns the external id bound for that channel, or "" if unbound.
func (s *Store) GetExternalID(ctx context.Context, threadID, channelName string) (string, error) {
	var externalID string
	err := s.db.QueryRowContext(ctx,
		`SELECT external_id FROM help_bindings WHERE thread_id = ? AND channel_name = ?`,
		threadID, channelName).Scan(&externalID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return externalID, err
}

// FindAskAnswer looks up an agent ask on this run identified by askKey that
// has since received a user reply in the same thread, and returns the
// earliest such reply. Used for idempotent replay of generic steps.
func (s *Store) FindAskAnswer(ctx context.Context, runID, askKey string) (answer, threadID string, found bool, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT m2.body, t.id
		FROM help_threads t
		JOIN help_messages m1 ON m1.thread_id = t.id AND m1.author = 'agent' AND m1.ask_key = ?
		JOIN help_messages m2 ON m2.thread_id = t.id AND m2.author = 'user' AND m2.created_at > m1.created_at
		WHERE t.run_id = ?
		ORDER BY m2.created_at ASC, m2.id ASC
		LIMIT 1`,
		askKey, runID)
	err = row.Scan(&answer, &threadID)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return answer, threadID, true, nil
}
