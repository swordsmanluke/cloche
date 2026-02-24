package evolution

import (
	"context"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// Collector gathers all data needed for evolution analysis.
type Collector struct {
	ProjectDir   string
	WorkflowName string
}

// Collect gathers runs, captures, knowledge base, prompts, and workflow.
// If stores are nil (for testing), DB reads are skipped.
func (c *Collector) Collect(ctx context.Context, evoStore ports.EvolutionStore, capStore ports.CaptureStore) (*CollectedData, error) {
	data := &CollectedData{
		ProjectDir:     c.ProjectDir,
		WorkflowName:   c.WorkflowName,
		Captures:       make(map[string][]*domain.StepExecution),
		CurrentPrompts: make(map[string]string),
	}

	// 1. Read knowledge base
	kbPath := filepath.Join(c.ProjectDir, ".cloche", "evolution", "knowledge", c.WorkflowName+".md")
	if kb, err := os.ReadFile(kbPath); err == nil {
		data.KnowledgeBase = string(kb)
	}

	// 2. Read workflow file
	wfPath := filepath.Join(c.ProjectDir, c.WorkflowName+".cloche")
	if wf, err := os.ReadFile(wfPath); err == nil {
		data.CurrentWorkflow = string(wf)
		data.WorkflowPath = wfPath
	}

	// 3. Extract prompt file references from workflow and read them
	promptRefs := extractPromptFiles(data.CurrentWorkflow)
	for _, ref := range promptRefs {
		fullPath := filepath.Join(c.ProjectDir, ref)
		if content, err := os.ReadFile(fullPath); err == nil {
			data.CurrentPrompts[ref] = string(content)
		}
	}

	// 4. Get runs from store (if available)
	if evoStore != nil {
		var sinceRunID string
		if last, err := evoStore.GetLastEvolution(ctx, c.ProjectDir, c.WorkflowName); err == nil && last != nil {
			sinceRunID = last.TriggerRunID
		}
		runs, err := evoStore.ListRunsSince(ctx, c.ProjectDir, c.WorkflowName, sinceRunID)
		if err != nil {
			return nil, err
		}
		data.Runs = runs

		// 5. Get captures for each run
		if capStore != nil {
			for _, run := range runs {
				caps, err := capStore.GetCaptures(ctx, run.ID)
				if err != nil {
					continue
				}
				data.Captures[run.ID] = caps
			}
		}
	}

	return data, nil
}

// extractPromptFiles finds file("path") references in workflow text.
func extractPromptFiles(workflow string) []string {
	re := regexp.MustCompile(`file\("([^"]+)"\)`)
	matches := re.FindAllStringSubmatch(workflow, -1)
	var files []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			files = append(files, m[1])
		}
	}
	return files
}
