package sqlite

import (
	"context"
	"time"

	"github.com/swordsmanluke/cloche/internal/signal"
)

// SaveSignal implements signal.Store.
func (s *Store) SaveSignal(ctx context.Context, projectDir string, sig signal.Signal) (int64, error) {
	res, err := s.write.ExecContext(ctx,
		`INSERT INTO signals (project_dir, source, key, severity, title, evidence, domain, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		projectDir, sig.Source, sig.Key, sig.Severity, sig.Title, sig.Evidence, sig.Domain, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSignals returns a project's stored signals, oldest first.
func (s *Store) ListSignals(ctx context.Context, projectDir string) ([]signal.Record, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT id, source, key, severity, title, evidence, domain, received_at
		 FROM signals WHERE project_dir = ? ORDER BY id`, projectDir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []signal.Record
	for rows.Next() {
		r := signal.Record{ProjectDir: projectDir}
		var at string
		if err := rows.Scan(&r.ID, &r.Signal.Source, &r.Signal.Key, &r.Signal.Severity,
			&r.Signal.Title, &r.Signal.Evidence, &r.Signal.Domain, &at); err != nil {
			return nil, err
		}
		r.ReceivedAt = parseTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
