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

// validateScriptPath checks that a script path is constrained to allowed directories.
func validateScriptPath(p string) error {
	if strings.Contains(p, "..") {
		return fmt.Errorf("path must not contain '..'")
	}

	cleaned := filepath.Clean(p)
	if strings.HasPrefix(cleaned, "scripts/") || strings.HasPrefix(cleaned, ".cloche/scripts/") {
		return nil
	}
	return fmt.Errorf("path must start with scripts/ or .cloche/scripts/")
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

	// Validate path is within allowed directories
	if err := validateScriptPath(resp.Path); err != nil {
		return nil, fmt.Errorf("invalid script path %q: %w", resp.Path, err)
	}

	// Validate content starts with a shebang line
	if !strings.HasPrefix(resp.Content, "#!") {
		return nil, fmt.Errorf("script content must start with a shebang line (e.g. #!/bin/bash)")
	}

	// Resolve full path and verify it stays within projectDir
	fullPath := filepath.Join(projectDir, resp.Path)
	resolved, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, fmt.Errorf("resolving script path: %w", err)
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}
	if !strings.HasPrefix(resolved, absProject+string(filepath.Separator)) {
		return nil, fmt.Errorf("script path %q resolves outside project directory", resp.Path)
	}

	// Check if script already exists — do not overwrite
	if _, err := os.Stat(fullPath); err == nil {
		return nil, fmt.Errorf("script %q already exists; will not overwrite", resp.Path)
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("creating script directory: %w", err)
	}

	// Write as non-executable; the workflow engine handles execution
	if err := os.WriteFile(fullPath, []byte(resp.Content), 0644); err != nil {
		return nil, fmt.Errorf("writing script file: %w", err)
	}

	return &GeneratedScript{Path: resp.Path, Content: resp.Content}, nil
}
