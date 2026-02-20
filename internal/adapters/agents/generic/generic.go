package generic

import (
	"context"
	"os/exec"

	"github.com/cloche-dev/cloche/internal/domain"
)

type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) Name() string {
	return "generic"
}

func (a *Adapter) Execute(ctx context.Context, step *domain.Step, workDir string) (string, error) {
	cmdStr := step.Config["run"]
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = workDir

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return resultOrDefault(step.Results, "fail"), nil
		}
		return "", err
	}

	return resultOrDefault(step.Results, "success"), nil
}

func resultOrDefault(results []string, name string) string {
	for _, r := range results {
		if r == name {
			return r
		}
	}
	if len(results) > 0 {
		return results[0]
	}
	return name
}
