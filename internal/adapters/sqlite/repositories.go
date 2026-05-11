package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/cloche-dev/cloche/internal/domain"
)

// migrateRepositoriesTable creates the repositories table if it does not exist.
// Called as part of the global schema migration sequence in migrate().
func migrateRepositoriesTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS repositories (
		project_dir TEXT NOT NULL,
		name        TEXT NOT NULL,
		path        TEXT NOT NULL DEFAULT '.',
		remote_url  TEXT NOT NULL DEFAULT '',
		is_default  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (project_dir, name)
	)`)
	return err
}

// ListRepositories returns all stored repositories for the given project.
// On first access for a project with no rows, a single default repository
// pointing at the project root (".") is seeded and returned automatically.
func (s *Store) ListRepositories(projectDir string) ([]domain.Repository, error) {
	rows, err := s.db.Query(
		`SELECT name, path, remote_url, is_default FROM repositories WHERE project_dir = ? ORDER BY name`,
		projectDir,
	)
	if err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}
	defer rows.Close()

	var repos []domain.Repository
	for rows.Next() {
		var r domain.Repository
		var isDefault int
		if err := rows.Scan(&r.Name, &r.Path, &r.RemoteURL, &isDefault); err != nil {
			return nil, fmt.Errorf("scanning repository row: %w", err)
		}
		r.IsDefault = isDefault == 1
		repos = append(repos, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating repository rows: %w", err)
	}

	if len(repos) == 0 {
		seed := domain.Repository{
			Name:      "default",
			Path:      projectDir,
			IsDefault: true,
		}
		if err := s.SaveRepository(projectDir, seed); err != nil {
			return nil, fmt.Errorf("auto-seeding default repository: %w", err)
		}
		repos = []domain.Repository{seed}
	}

	return repos, nil
}

// SaveRepository upserts a repository record for the project.
func (s *Store) SaveRepository(projectDir string, repo domain.Repository) error {
	_, err := s.db.Exec(
		`INSERT INTO repositories (project_dir, name, path, remote_url, is_default)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_dir, name) DO UPDATE SET
		   path       = excluded.path,
		   remote_url = excluded.remote_url,
		   is_default = excluded.is_default`,
		projectDir, repo.Name, repo.Path, repo.RemoteURL, boolToInt(repo.IsDefault),
	)
	if err != nil {
		return fmt.Errorf("saving repository %q: %w", repo.Name, err)
	}
	return nil
}

// GetRepository returns a single repository by name within a project, or nil if not found.
func (s *Store) GetRepository(projectDir, name string) (*domain.Repository, error) {
	row := s.db.QueryRow(
		`SELECT name, path, remote_url, is_default FROM repositories WHERE project_dir = ? AND name = ?`,
		projectDir, name,
	)
	var r domain.Repository
	var isDefault int
	if err := row.Scan(&r.Name, &r.Path, &r.RemoteURL, &isDefault); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting repository %q: %w", name, err)
	}
	r.IsDefault = isDefault == 1
	return &r, nil
}

// DeleteRepository removes a repository record for the project.
func (s *Store) DeleteRepository(projectDir, name string) error {
	_, err := s.db.Exec(
		`DELETE FROM repositories WHERE project_dir = ? AND name = ?`,
		projectDir, name,
	)
	if err != nil {
		return fmt.Errorf("deleting repository %q: %w", name, err)
	}
	return nil
}
