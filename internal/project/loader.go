package project

import (
	"fmt"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
)

// Load reads the project configuration from <dir>/.cloche/config.toml and returns
// a domain.Project populated with the declared repositories.
//
// Repository paths are stored as declared in config.toml; they are interpreted
// relative to dir (the project root), not relative to .cloche/.
//
// If config.toml is absent, Load returns a Project with no repositories (not an error).
// If config.toml is malformed, Load returns a wrapped error.
func Load(dir string) (*domain.Project, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("project.Load %s: %w", dir, err)
	}

	var repos []domain.Repository
	for _, r := range cfg.Repositories {
		repos = append(repos, domain.Repository{
			Name:      r.Name,
			Path:      r.Path,
			RemoteURL: r.URL,
		})
	}

	return &domain.Project{
		Dir:          dir,
		Repositories: repos,
	}, nil
}
