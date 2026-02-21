package generic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
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

	// Extract result marker before writing logs
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write cleaned output to log file
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, step.Name+".log"), cleanOutput, 0644)
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			if found {
				return markerResult, nil
			}
			return "fail", nil
		}
		return "", err
	}

	if found {
		return markerResult, nil
	}
	return "success", nil
}
