package sqlite

import (
	"context"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// SaveStepInput implements ports.StepInputStore. The attempt number is
// assigned here — one past the step's highest recorded attempt in this run —
// inside a single statement so two writers for the same step cannot race to
// the same number.
func (s *Store) SaveStepInput(ctx context.Context, in *domain.StepInput) error {
	now := time.Now()
	res, err := s.write.ExecContext(ctx,
		`INSERT INTO step_inputs (run_id, step_name, attempt, kind, content, created_at)
		 VALUES (?, ?, (SELECT COALESCE(MAX(attempt), 0) + 1 FROM step_inputs WHERE run_id = ? AND step_name = ?), ?, ?, ?)`,
		in.RunID, in.StepName, in.RunID, in.StepName, string(in.Kind), in.Content, formatTime(now))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	var attempt int
	if err := s.read.QueryRowContext(ctx, `SELECT attempt FROM step_inputs WHERE id = ?`, id).Scan(&attempt); err != nil {
		return err
	}
	in.Attempt = attempt
	in.CreatedAt = now
	return nil
}

// ListStepInputs implements ports.StepInputStore.
func (s *Store) ListStepInputs(ctx context.Context, runID, stepName string) ([]*domain.StepInput, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT attempt, kind, content, created_at FROM step_inputs
		 WHERE run_id = ? AND step_name = ? ORDER BY attempt`, runID, stepName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.StepInput
	for rows.Next() {
		in := &domain.StepInput{RunID: runID, StepName: stepName}
		var kind, created string
		if err := rows.Scan(&in.Attempt, &kind, &in.Content, &created); err != nil {
			return nil, err
		}
		in.Kind = domain.StepInputKind(kind)
		in.CreatedAt = parseTime(created)
		out = append(out, in)
	}
	return out, rows.Err()
}
