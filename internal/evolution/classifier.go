package evolution

import (
	"context"
	"encoding/json"
	"strings"
)

// Classifier categorizes run prompts.
type Classifier struct {
	LLM LLMClient
}

type classifyResponse struct {
	Classification string `json:"classification"`
}

// Classify categorizes a run prompt into: bug, feedback, feature, enhancement, chore.
func (c *Classifier) Classify(ctx context.Context, runPrompt string) (string, error) {
	systemPrompt := `You are a classifier for software development tasks. Given a task description, classify it into exactly one category:

- bug: fixing something broken, a defect, vulnerability, or regression
- feedback: code review style issues (DRY violations, SOLID principles, architectural concerns, style issues)
- feature: new functionality being added
- enhancement: improving existing functionality
- chore: maintenance tasks, dependency updates, CI changes

Respond with JSON: {"classification": "<category>"}
Do not include any other text.`

	response, err := c.LLM.Complete(ctx, systemPrompt, runPrompt)
	if err != nil {
		return "feature", nil // default on error
	}

	var resp classifyResponse
	// Try to parse JSON from the response - it might have extra text
	response = strings.TrimSpace(response)
	if err := json.Unmarshal([]byte(response), &resp); err != nil {
		return "feature", nil // default on parse error
	}

	// Validate the classification
	switch resp.Classification {
	case "bug", "feedback", "feature", "enhancement", "chore":
		return resp.Classification, nil
	default:
		return "feature", nil
	}
}
