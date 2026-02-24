package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Curator merges lessons into prompt files using ACE-style curation.
type Curator struct {
	LLM   LLMClient
	Audit *AuditLogger
}

// Apply curates a lesson into the target prompt file.
func (c *Curator) Apply(ctx context.Context, projectDir string, lesson *Lesson) (*Change, error) {
	targetPath := filepath.Join(projectDir, lesson.Target)
	current, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("reading target prompt %s: %w", lesson.Target, err)
	}

	systemPrompt := `You are a prompt curator using ACE (Agentic Context Engineering) principles.
Your job is to merge a new lesson into an existing prompt document.

Rules:
- Append the lesson as a structured bullet/rule in a "## Learned Rules" section
- If a "## Learned Rules" section already exists, add to it
- If the lesson refines or duplicates an existing rule, update in place rather than appending
- Preserve ALL existing content exactly as-is
- Keep rules concise and actionable
- Do not add commentary — return only the updated prompt content`

	userPrompt := fmt.Sprintf("## Current Prompt Content\n```\n%s\n```\n\n## Lesson to Merge\nInsight: %s\nSuggested Action: %s",
		string(current), lesson.Insight, lesson.SuggestedAction)

	updated, err := c.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("curator LLM call: %w", err)
	}

	// Snapshot before writing
	var snapName string
	if c.Audit != nil {
		snapName, _ = c.Audit.Snapshot(lesson.Target)
	}

	if err := os.WriteFile(targetPath, []byte(updated), 0644); err != nil {
		return nil, fmt.Errorf("writing updated prompt: %w", err)
	}

	return &Change{
		Type:     "prompt_update",
		File:     lesson.Target,
		Reason:   lesson.Insight,
		Snapshot: snapName,
	}, nil
}
