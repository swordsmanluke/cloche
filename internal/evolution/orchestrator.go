package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/ports"
)

// OrchestratorConfig configures the evolution pipeline.
type OrchestratorConfig struct {
	ProjectDir    string
	WorkflowName  string
	LLM           LLMClient
	MinConfidence string
}

// Orchestrator wires all evolution pipeline stages together.
type Orchestrator struct {
	cfg        OrchestratorConfig
	collector  *Collector
	classifier *Classifier
	reflector  *Reflector
	curator    *Curator
	scriptGen  *ScriptGenerator
	mutator    *dsl.Mutator
	audit      *AuditLogger
}

// NewOrchestrator creates a fully wired evolution pipeline.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	audit := &AuditLogger{ProjectDir: cfg.ProjectDir}
	return &Orchestrator{
		cfg:        cfg,
		collector:  &Collector{ProjectDir: cfg.ProjectDir, WorkflowName: cfg.WorkflowName},
		classifier: &Classifier{LLM: cfg.LLM},
		reflector:  &Reflector{LLM: cfg.LLM, MinConfidence: cfg.MinConfidence},
		curator:    &Curator{LLM: cfg.LLM, Audit: audit},
		scriptGen:  &ScriptGenerator{LLM: cfg.LLM},
		mutator:    &dsl.Mutator{},
		audit:      audit,
	}
}

// Run executes the full evolution pipeline.
func (o *Orchestrator) Run(ctx context.Context, triggerRunID string, evoStore ports.EvolutionStore, capStore ports.CaptureStore) (*EvolutionResult, error) {
	// Stage 1: Collect
	data, err := o.collector.Collect(ctx, evoStore, capStore)
	if err != nil {
		return nil, fmt.Errorf("collector: %w", err)
	}

	// Stage 2: Classify the triggering run
	var runPrompt string
	if evoStore != nil {
		// Find the triggering run's prompt from captures
		if caps, ok := data.Captures[triggerRunID]; ok {
			for _, cap := range caps {
				if cap.PromptText != "" {
					runPrompt = cap.PromptText
					break
				}
			}
		}
	}
	classification, err := o.classifier.Classify(ctx, runPrompt)
	if err != nil {
		return nil, fmt.Errorf("classifier: %w", err)
	}

	// Stage 3: Reflect
	lessons, err := o.reflector.Reflect(ctx, data, classification)
	if err != nil {
		return nil, fmt.Errorf("reflector: %w", err)
	}

	result := &EvolutionResult{
		ID:             fmt.Sprintf("evo-%d", time.Now().UnixNano()),
		ProjectDir:     o.cfg.ProjectDir,
		WorkflowName:   o.cfg.WorkflowName,
		TriggerRunID:   triggerRunID,
		Timestamp:      time.Now().Format(time.RFC3339),
		Classification: classification,
	}

	if len(lessons) == 0 {
		// No actionable lessons — log and return
		o.audit.Log(result)
		return result, nil
	}

	// Stage 4: Execute branches based on lesson category
	for _, lesson := range lessons {
		switch lesson.Category {
		case "prompt_improvement":
			change, err := o.curator.Apply(ctx, o.cfg.ProjectDir, &lesson)
			if err != nil {
				continue // log but don't fail the whole pipeline
			}
			result.Changes = append(result.Changes, *change)

		case "new_step":
			if err := o.handleNewStep(ctx, data, &lesson, result); err != nil {
				continue
			}
		}
	}

	// Stage 5: Audit
	o.audit.UpdateKnowledge(o.cfg.WorkflowName, lessons)
	result.KnowledgeDelta = fmt.Sprintf("%d lessons applied", len(lessons))
	o.audit.Log(result)

	// Save to store if available
	if evoStore != nil {
		evoStore.SaveEvolution(ctx, &ports.EvolutionEntry{
			ID:             result.ID,
			ProjectDir:     result.ProjectDir,
			WorkflowName:   result.WorkflowName,
			TriggerRunID:   result.TriggerRunID,
			CreatedAt:      time.Now(),
			Classification: result.Classification,
			ChangesJSON:    fmt.Sprintf("%d changes", len(result.Changes)),
			KnowledgeDelta: result.KnowledgeDelta,
		})
	}

	return result, nil
}

// handleNewStep generates a script/prompt file and adds the step + wiring to the workflow.
func (o *Orchestrator) handleNewStep(ctx context.Context, data *CollectedData, lesson *Lesson, result *EvolutionResult) error {
	// Generate the script or prompt file
	generated, err := o.scriptGen.Generate(ctx, o.cfg.ProjectDir, lesson)
	if err != nil {
		return err
	}

	result.Changes = append(result.Changes, Change{
		Type:   "add_script",
		File:   generated.Path,
		Reason: lesson.Insight,
	})

	// Add step and wiring to the workflow file
	if data.WorkflowPath == "" {
		return nil
	}

	workflowContent, err := os.ReadFile(data.WorkflowPath)
	if err != nil {
		return err
	}

	// Snapshot the workflow
	wfRelPath, _ := filepath.Rel(o.cfg.ProjectDir, data.WorkflowPath)
	snapName, _ := o.audit.Snapshot(wfRelPath)

	// Derive step name from the script path
	stepName := filepath.Base(generated.Path)
	stepName = stepName[:len(stepName)-len(filepath.Ext(stepName))]

	// Determine the config based on step type
	config := map[string]string{}
	switch lesson.StepType {
	case "agent":
		config["prompt"] = fmt.Sprintf(`file("%s")`, generated.Path)
	default: // script
		config["run"] = fmt.Sprintf(`"%s"`, generated.Path)
	}

	// Add the step
	updated, err := o.mutator.AddStep(string(workflowContent), dsl.StepDef{
		Name:    stepName,
		Type:    lesson.StepType,
		Config:  config,
		Results: []string{"success", "fail"},
	})
	if err != nil {
		return fmt.Errorf("adding step to workflow: %w", err)
	}

	// Add wiring: wire the new step's fail to the fix step (if it exists),
	// and success to done
	wires := []dsl.WireDef{
		{From: stepName, Result: "success", To: "done"},
		{From: stepName, Result: "fail", To: "abort"},
	}
	updated, err = o.mutator.AddWiring(updated, wires)
	if err != nil {
		return fmt.Errorf("adding wiring to workflow: %w", err)
	}

	if err := os.WriteFile(data.WorkflowPath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("writing updated workflow: %w", err)
	}

	result.Changes = append(result.Changes, Change{
		Type:     "add_step",
		File:     wfRelPath,
		Reason:   lesson.Insight,
		Snapshot: snapName,
	})

	return nil
}
