package generic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

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

	output, err := cmd.CombinedOutput()

	// Write captured output to .cloche/output/<step-name>.log
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, step.Name+".log"), output, 0644)
	}

	if err != nil {
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
