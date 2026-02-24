package dsl

import (
	"fmt"
	"regexp"
	"strings"
)

// StepDef describes a step to add to a workflow.
type StepDef struct {
	Name    string
	Type    string // "script" or "agent"
	Config  map[string]string
	Results []string
}

// WireDef describes a wire to add.
type WireDef struct {
	From   string
	Result string
	To     string
}

// CollectAddition describes a condition to add to an existing collect clause.
type CollectAddition struct {
	CollectTarget string
	Step          string
	Result        string
}

// Mutator provides additive operations on workflow DSL text.
type Mutator struct{}

// AddStep inserts a new step definition into the workflow text.
// It inserts after the last step block, before the wiring section.
func (m *Mutator) AddStep(input string, step StepDef) (string, error) {
	// Build the step DSL text
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n  step %s {\n", step.Name))

	// Write the type-specific config key first
	switch step.Type {
	case "script":
		if run, ok := step.Config["run"]; ok {
			sb.WriteString(fmt.Sprintf("    run = %s\n", run))
		}
	case "agent":
		if prompt, ok := step.Config["prompt"]; ok {
			sb.WriteString(fmt.Sprintf("    prompt = %s\n", prompt))
		}
	}

	// Write other config keys
	for k, v := range step.Config {
		if k == "run" || k == "prompt" {
			continue
		}
		sb.WriteString(fmt.Sprintf("    %s = %s\n", k, v))
	}

	// Write results
	if len(step.Results) > 0 {
		sb.WriteString(fmt.Sprintf("    results = [%s]\n", strings.Join(step.Results, ", ")))
	}
	sb.WriteString("  }\n")

	stepText := sb.String()

	// Find the position after the last step block's closing brace.
	// We look for the pattern of a step closing brace (indented "}")
	// followed by either a blank line, a wire, a collect, or the workflow closing brace.
	// Strategy: find the last "step NAME {" and then its matching "}"
	re := regexp.MustCompile(`(?m)(^  \})\n`)
	matches := re.FindAllStringIndex(input, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("could not find step block closing brace in workflow")
	}

	// Find the last step block by searching for "step " keyword before each closing brace
	lastStepEnd := -1
	for i := len(matches) - 1; i >= 0; i-- {
		// Check if there's a "step " keyword before this closing brace
		prefix := input[:matches[i][0]]
		lastStep := strings.LastIndex(prefix, "  step ")
		if lastStep >= 0 {
			lastStepEnd = matches[i][1]
			break
		}
	}

	if lastStepEnd == -1 {
		return "", fmt.Errorf("could not find last step block in workflow")
	}

	result := input[:lastStepEnd] + stepText + input[lastStepEnd:]

	// Validate the result
	if _, err := Parse(result); err != nil {
		return "", fmt.Errorf("validation failed after adding step: %w", err)
	}

	return result, nil
}

// AddWiring appends wire lines to the workflow text.
// New wires are inserted before the closing brace of the workflow.
func (m *Mutator) AddWiring(input string, wires []WireDef) (string, error) {
	var sb strings.Builder
	for _, w := range wires {
		sb.WriteString(fmt.Sprintf("  %s:%s -> %s\n", w.From, w.Result, w.To))
	}

	// Find the last closing brace (the workflow's closing brace)
	lastBrace := strings.LastIndex(input, "}")
	if lastBrace == -1 {
		return "", fmt.Errorf("could not find workflow closing brace")
	}

	// Insert before the closing brace
	result := input[:lastBrace] + sb.String() + input[lastBrace:]

	if _, err := Parse(result); err != nil {
		return "", fmt.Errorf("validation failed after adding wiring: %w", err)
	}

	return result, nil
}

// UpdateCollect adds a condition to an existing collect clause.
func (m *Mutator) UpdateCollect(input string, addition CollectAddition) (string, error) {
	// Find the collect clause targeting the specified step
	// Pattern: collect all/any(...) -> target
	pattern := fmt.Sprintf(`(collect\s+(?:all|any)\()([^)]*?)(\)\s*->\s*%s)`, regexp.QuoteMeta(addition.CollectTarget))
	re := regexp.MustCompile(pattern)

	match := re.FindStringIndex(input)
	if match == nil {
		return "", fmt.Errorf("could not find collect clause targeting %q", addition.CollectTarget)
	}

	submatches := re.FindStringSubmatch(input)
	if len(submatches) < 4 {
		return "", fmt.Errorf("could not parse collect clause")
	}

	// Add the new condition
	existingConditions := strings.TrimSpace(submatches[2])
	newCondition := fmt.Sprintf("%s:%s", addition.Step, addition.Result)
	updatedConditions := existingConditions + ", " + newCondition

	replacement := submatches[1] + updatedConditions + submatches[3]
	result := input[:match[0]] + replacement + input[match[1]:]

	if _, err := Parse(result); err != nil {
		return "", fmt.Errorf("validation failed after updating collect: %w", err)
	}

	return result, nil
}
