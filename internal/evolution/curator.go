package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Curator merges lessons into prompt files using ACE-style curation.
type Curator struct {
	LLM   LLMClient
	Audit *AuditLogger
}

// lessonAlreadyPresent checks whether the lesson's key insight or action is
// already present in the current prompt text (case-insensitive substring match).
func lessonAlreadyPresent(currentPrompt string, lesson *Lesson) bool {
	lower := strings.ToLower(currentPrompt)
	if lesson.Insight != "" && strings.Contains(lower, strings.ToLower(lesson.Insight)) {
		return true
	}
	if lesson.SuggestedAction != "" && strings.Contains(lower, strings.ToLower(lesson.SuggestedAction)) {
		return true
	}
	return false
}

// Apply curates a lesson into the target prompt file.
func (c *Curator) Apply(ctx context.Context, projectDir string, lesson *Lesson) (*Change, error) {
	targetPath := filepath.Join(projectDir, lesson.Target)
	current, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("reading target prompt %s: %w", lesson.Target, err)
	}

	// Guard: skip if the lesson is already incorporated in the prompt
	if lessonAlreadyPresent(string(current), lesson) {
		return nil, nil
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

	raw, err := c.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("curator LLM call: %w", err)
	}

	updated := stripCodeFences(raw)

	// Validate that the LLM output is actual prompt content, not meta-conversation
	if isConversationalResponse(updated) {
		// Fallback: append the lesson directly rather than trusting the LLM
		updated = appendLessonDirectly(string(current), lesson)
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

// conversationalMarkers are patterns that indicate the LLM returned
// meta-conversation text instead of prompt content.
var conversationalMarkers = regexp.MustCompile(
	`(?i)^(I need |I can't |I cannot |Could you |Please grant |` +
		`I don't have |I do not have |permission|write access|blocked by|` +
		`Here is the updated|Here's the updated|I've updated|I have updated|` +
		`Let me |I'll |I will )`)

// isConversationalResponse checks whether the LLM output appears to be
// meta-conversation text rather than actual prompt content.
func isConversationalResponse(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return true
	}
	// Check the first non-empty line for conversational markers
	firstLine := trimmed
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		firstLine = trimmed[:idx]
	}
	return conversationalMarkers.MatchString(strings.TrimSpace(firstLine))
}

// appendLessonDirectly adds a lesson to the prompt content without LLM help,
// used as a fallback when the LLM returns conversational text.
func appendLessonDirectly(current string, lesson *Lesson) string {
	current = strings.TrimRight(current, "\n")
	if strings.Contains(current, "## Learned Rules") {
		return current + fmt.Sprintf("\n- %s: %s\n", lesson.Insight, lesson.SuggestedAction)
	}
	return current + fmt.Sprintf("\n\n## Learned Rules\n\n- %s: %s\n", lesson.Insight, lesson.SuggestedAction)
}

// stripCodeFences removes markdown code fences from an LLM response,
// extracting just the content between them. If no fences are found,
// the input is returned unchanged (trimmed).
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)

	openIdx := strings.Index(s, "```")
	if openIdx == -1 {
		return s
	}

	// Skip the opening fence line (may include language hint like ```markdown)
	afterOpen := s[openIdx+3:]
	newlineIdx := strings.Index(afterOpen, "\n")
	if newlineIdx == -1 {
		return s
	}
	contentStart := openIdx + 3 + newlineIdx + 1

	// Find the closing fence (last ``` in the string)
	closeIdx := strings.LastIndex(s, "```")
	if closeIdx <= openIdx {
		return s
	}

	return s[contentStart:closeIdx]
}
