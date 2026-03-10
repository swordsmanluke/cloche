package generic

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
)

type Adapter struct {
	StatusWriter *protocol.StatusWriter // optional: streams live output lines
}

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

	var output []byte
	var err error

	if a.StatusWriter != nil {
		output, err = a.executeStreaming(cmd, step.Name)
	} else {
		output, err = cmd.CombinedOutput()
	}

	// Extract result marker before writing logs
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write cleaned output to log file
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, step.Name+".log"), cleanOutput, 0644)
	}

	isAgent := step.Type == domain.StepTypeAgent

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			result := "fail"
			if found {
				result = markerResult
			}
			protocol.AppendHistory(workDir, step.Name, result, isAgent, cleanOutput)
			return result, nil
		}
		return "", err
	}

	result := "success"
	if found {
		result = markerResult
	}
	protocol.AppendHistory(workDir, step.Name, result, isAgent, cleanOutput)
	return result, nil
}

// executeStreaming runs the command and streams output lines through StatusWriter
// in real-time. Returns the full accumulated output.
func (a *Adapter) executeStreaming(cmd *exec.Cmd, stepName string) ([]byte, error) {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout pipe

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		a.StatusWriter.Log(stepName, line)
	}

	waitErr := cmd.Wait()
	return buf.Bytes(), waitErr
}
