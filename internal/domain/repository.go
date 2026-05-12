package domain

// Repository represents a named source-code repository declared in a project's config.
// Path is stored as declared in config.toml (relative to the project root, not .cloche/).
// IsDefault indicates the repository that runs use when no explicit repository is specified.
type Repository struct {
	Name      string
	Path      string
	RemoteURL string
	IsDefault bool
}

// Project is the in-memory description of a cloche project, including its declared repositories.
// It is populated by the project loader and consumed by the runtime adapter.
type Project struct {
	Dir          string
	Repositories []Repository
}
