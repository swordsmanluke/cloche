package ports

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

// AgentAdapter executes a single agent step inside the container.
type AgentAdapter interface {
	Name() string
	Execute(ctx context.Context, step *domain.Step, workDir string) (result string, err error)
}
