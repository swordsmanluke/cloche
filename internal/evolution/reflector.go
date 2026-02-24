package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Reflector examines execution traces and extracts structured lessons.
type Reflector struct {
	LLM           LLMClient
	MinConfidence string // "low", "medium", "high"
}

type reflectResponse struct {
	Lessons []Lesson `json:"lessons"`
}

// confidenceLevel returns a numeric level for comparison.
func confidenceLevel(c string) int {
	switch c {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// Reflect analyzes collected data and returns actionable lessons.
func (r *Reflector) Reflect(ctx context.Context, data *CollectedData, classification string) ([]Lesson, error) {
	systemPrompt := `You are an evolution agent that analyzes software development workflow execution history.
You examine run results, failure patterns, retry counts, and user feedback to extract structured lessons.

For each lesson, provide:
- id: a unique identifier (e.g., "lesson-YYYYMMDD-NNN")
- category: one of "prompt_improvement" or "new_step"
- step_type: for new_step, either "script" or "agent"
- target: for prompt_improvement, the prompt file path to update
- insight: what pattern you observed
- suggested_action: the concrete change to make
- evidence: list of run IDs that support this lesson
- confidence: "high" (4+ occurrences, clear pattern), "medium" (2-3 occurrences), "low" (1 occurrence or ambiguous)

Only suggest changes that address real, repeated patterns. Do not suggest changes for one-off issues.
Respond with JSON: {"lessons": [...]}
Do not include any other text.`

	// Build the user prompt with all collected data
	var parts []string
	parts = append(parts, fmt.Sprintf("## Classification\nThis analysis was triggered by a run classified as: %s", classification))

	if data.KnowledgeBase != "" {
		parts = append(parts, "## Current Knowledge Base\n"+data.KnowledgeBase)
	}

	if data.CurrentWorkflow != "" {
		parts = append(parts, "## Current Workflow\n```\n"+data.CurrentWorkflow+"\n```")
	}

	for path, content := range data.CurrentPrompts {
		parts = append(parts, fmt.Sprintf("## Prompt: %s\n```\n%s\n```", path, content))
	}

	if len(data.Runs) > 0 {
		parts = append(parts, "## Run History")
		for _, run := range data.Runs {
			runInfo := fmt.Sprintf("### Run %s (workflow: %s, state: %s)", run.ID, run.WorkflowName, run.State)
			if caps, ok := data.Captures[run.ID]; ok {
				for _, cap := range caps {
					stepInfo := fmt.Sprintf("- Step %s: result=%s", cap.StepName, cap.Result)
					if cap.AttemptNumber > 1 {
						stepInfo += fmt.Sprintf(" (attempt %d)", cap.AttemptNumber)
					}
					if cap.Logs != "" {
						stepInfo += "\n  Logs: " + truncate(cap.Logs, 500)
					}
					runInfo += "\n" + stepInfo
				}
			}
			parts = append(parts, runInfo)
		}
	}

	userPrompt := strings.Join(parts, "\n\n")

	response, err := r.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("reflector LLM call: %w", err)
	}

	var resp reflectResponse
	response = strings.TrimSpace(response)
	if err := json.Unmarshal([]byte(response), &resp); err != nil {
		return nil, fmt.Errorf("parsing reflector response: %w", err)
	}

	// Filter by minimum confidence
	minLevel := confidenceLevel(r.MinConfidence)
	var filtered []Lesson
	for _, l := range resp.Lessons {
		if confidenceLevel(l.Confidence) >= minLevel {
			filtered = append(filtered, l)
		}
	}

	return filtered, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
