package evolution

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

// LLMClient abstracts LLM calls for evolution stages.
type LLMClient interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// CollectedData is the input to the Classifier and Reflector.
type CollectedData struct {
	Runs            []*domain.Run
	Captures        map[string][]*domain.StepExecution // run_id -> step executions
	KnowledgeBase   string                             // contents of knowledge/<workflow>.md
	CurrentPrompts  map[string]string                  // relative path -> content
	CurrentWorkflow string                             // .cloche file content
	WorkflowPath    string                             // path to .cloche file
	ProjectDir      string
	WorkflowName    string
}

// Lesson is a structured insight extracted by the Reflector.
type Lesson struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"`
	StepType        string   `json:"step_type,omitempty"`
	Target          string   `json:"target,omitempty"`
	Insight         string   `json:"insight"`
	SuggestedAction string   `json:"suggested_action"`
	Evidence        []string `json:"evidence"`
	Confidence      string   `json:"confidence"`
}

// EvolutionResult records what an evolution pass produced.
type EvolutionResult struct {
	ID             string   `json:"id"`
	ProjectDir     string   `json:"project_dir"`
	WorkflowName   string   `json:"workflow_name"`
	TriggerRunID   string   `json:"trigger_run_id"`
	Timestamp      string   `json:"timestamp"`
	Classification string   `json:"classification"`
	Changes        []Change `json:"changes"`
	KnowledgeDelta string   `json:"knowledge_delta"`
}

// Change describes a single file modification made by evolution.
type Change struct {
	Type     string `json:"type"`
	File     string `json:"file"`
	Reason   string `json:"reason"`
	Snapshot string `json:"snapshot"`
}
