package domain

// Repository represents a named source-code repository declared in a project's config.
// Path is stored as declared in config.toml (relative to the project root, not .cloche/).
type Repository struct {
	Name      string
	Path      string
	RemoteURL string
}

// Project is the in-memory description of a cloche project, including its declared repositories.
// It is populated by the project loader and consumed by the runtime adapter.
type Project struct {
	Dir          string
	Repositories []Repository
}

// DefaultRepository returns the implicit default repository for a project:
//   - If exactly one repository is declared, it is the default.
//   - Otherwise nil is returned (caller must handle the no-default case).
func DefaultRepository(repos []Repository) *Repository {
	if len(repos) == 1 {
		return &repos[0]
	}
	return nil
}
