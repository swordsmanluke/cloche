package domain

// Repository represents a source code repository associated with a project.
type Repository struct {
	Name      string
	Path      string // path relative to the project root (e.g. "." or "./repos/backend")
	RemoteURL string
	IsDefault bool
}
