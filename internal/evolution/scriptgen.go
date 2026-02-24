package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScriptGenerator creates new checker/linter scripts via LLM code generation.
type ScriptGenerator struct {
	LLM LLMClient
}

type scriptResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// GeneratedScript holds the result of script generation.
type GeneratedScript struct {
	Path    string
	Content string
}

// Generate creates a script file based on the lesson.
func (g *ScriptGenerator) Generate(ctx context.Context, projectDir string, lesson *Lesson) (*GeneratedScript, error) {
	systemPrompt := `You are a script generator for software validation workflows.
Given a description of what needs to be checked, generate a shell script that performs the check.

Rules:
- The script should exit 0 on success, non-zero on failure
- Keep it simple and focused on one check
- Use standard tools available in a typical development container
- Include a shebang line (#!/bin/bash or #!/bin/sh)

Respond with JSON: {"path": "scripts/<name>.sh", "content": "<script content>"}
Use \n for newlines in the content field.
Do not include any other text.`

	userPrompt := fmt.Sprintf("Check needed: %s\nDetails: %s", lesson.Insight, lesson.SuggestedAction)

	response, err := g.LLM.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("script generator LLM call: %w", err)
	}

	var resp scriptResponse
	response = strings.TrimSpace(response)
	if err := json.Unmarshal([]byte(response), &resp); err != nil {
		return nil, fmt.Errorf("parsing script generator response: %w", err)
	}

	if resp.Path == "" || resp.Content == "" {
		return nil, fmt.Errorf("script generator returned empty path or content")
	}

	// Create parent directories
	fullPath := filepath.Join(projectDir, resp.Path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("creating script directory: %w", err)
	}

	// Write with executable permissions
	if err := os.WriteFile(fullPath, []byte(resp.Content), 0755); err != nil {
		return nil, fmt.Errorf("writing script file: %w", err)
	}

	return &GeneratedScript{Path: resp.Path, Content: resp.Content}, nil
}
