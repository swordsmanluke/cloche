package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupValidProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(filepath.Join(clocheDir, "prompts"), 0755)
	os.MkdirAll(filepath.Join(clocheDir, "scripts"), 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`active = true
[orchestration]
concurrency = 2
`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(`workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run = "go test ./..."
    results = [success, fail]
  }
  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> abort
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "prompts", "implement.md"), []byte("implement prompt"), 0644)

	return dir
}

func TestValidateProject_Valid(t *testing.T) {
	dir := setupValidProject(t)
	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateProject_NoClocheDir(t *testing.T) {
	dir := t.TempDir()
	errs := validateProject(dir, "")
	if len(errs) != 1 || !strings.Contains(errs[0], ".cloche directory not found") {
		t.Errorf("expected .cloche not found error, got: %v", errs)
	}
}

func TestValidateProject_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`invalid = [[[`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "config.toml") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected config.toml error, got: %v", errs)
	}
}

func TestValidateProject_InvalidWorkflowSyntax(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "bad.cloche"), []byte(`not a valid workflow`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "bad.cloche") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parse error for bad.cloche, got: %v", errs)
	}
}

func TestValidateProject_UnwiredResult(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step a {
    run = "echo hello"
    results = [success, fail]
  }
  a:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "not wired") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unwired result error, got: %v", errs)
	}
}

func TestValidateProject_OrphanStep(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step a {
    run = "echo a"
    results = [success]
  }
  step orphan {
    run = "echo orphan"
    results = [success]
  }
  a:success -> done
  orphan:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "orphan") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected orphan step error, got: %v", errs)
	}
}

func TestValidateProject_MissingPromptFile(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step impl {
    prompt = file("prompts/missing.md")
    results = [success, fail]
  }
  impl:success -> done
  impl:fail -> abort
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "missing file") && strings.Contains(e, "prompts/missing.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing file reference error, got: %v", errs)
	}
}

func TestValidateProject_MissingScript(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step prep {
    run = "bash .cloche/scripts/missing.sh"
    results = [success, fail]
  }
  prep:success -> done
  prep:fail -> abort
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "missing script") && strings.Contains(e, "missing.sh") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing script error, got: %v", errs)
	}
}

func TestValidateProject_WorkflowFilter(t *testing.T) {
	dir := setupValidProject(t)

	// Filter to existing workflow — should pass
	errs := validateProject(dir, "develop")
	if len(errs) > 0 {
		t.Errorf("expected no errors for valid workflow filter, got: %v", errs)
	}

	// Filter to non-existent workflow
	errs = validateProject(dir, "nonexistent")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected not found error for bad filter, got: %v", errs)
	}
}

func TestValidateProject_CrossFileWorkflowRef(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`active = true`), 0644)

	// Host workflow references a workflow that doesn't exist
	os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(`workflow "main" {
  host {}
  step dev {
    workflow_name = "nonexistent"
    results = [success, fail]
  }
  dev:success -> done
  dev:fail -> abort
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "undefined workflow") && strings.Contains(e, "nonexistent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected undefined workflow reference error, got: %v", errs)
	}
}

func TestValidateProject_ValidHostWithContainerRef(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`active = true`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(`workflow "develop" {
  step impl {
    run = "echo go"
    results = [success, fail]
  }
  impl:success -> done
  impl:fail -> abort
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(`workflow "main" {
  host {}
  step dev {
    workflow_name = "develop"
    results = [success, fail]
  }
  dev:success -> done
  dev:fail -> abort
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateProject_DuplicateWorkflowAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	wfContent := []byte(`workflow "dup" {
  step a {
    run = "echo a"
    results = [success]
  }
  a:success -> done
}`)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), wfContent, 0644)
	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), wfContent, 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "already defined") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate workflow error, got: %v", errs)
	}
}

func TestExtractFileRef(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`file("prompts/implement.md")`, "prompts/implement.md"},
		{`file("prompts/fix.md")`, "prompts/fix.md"},
		{`"just a string"`, ""},
		{`something_else`, ""},
		{`file("")`, ""},
	}

	for _, tt := range tests {
		got := extractFileRef(tt.input)
		if got != tt.expected {
			t.Errorf("extractFileRef(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractScriptRef(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`bash .cloche/scripts/prepare.sh`, ".cloche/scripts/prepare.sh"},
		{`.cloche/scripts/run.sh`, ".cloche/scripts/run.sh"},
		{`go test ./...`, ""},
		{`echo hello`, ""},
	}

	for _, tt := range tests {
		got := extractScriptRef(tt.input)
		if got != tt.expected {
			t.Errorf("extractScriptRef(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestValidateProject_ValidScriptRef(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(filepath.Join(clocheDir, "scripts"), 0755)

	os.WriteFile(filepath.Join(clocheDir, "scripts", "prepare.sh"), []byte("#!/bin/bash\necho hi"), 0644)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step prep {
    run = "bash .cloche/scripts/prepare.sh"
    results = [success, fail]
  }
  prep:success -> done
  prep:fail -> abort
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors for valid script ref, got: %v", errs)
	}
}

func TestValidateProject_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "test.cloche"), []byte(`workflow "test" {
  step a {
    run = "echo a"
    results = [success]
  }
  a:success -> done
}`), 0644)

	// Should pass even without config.toml
	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors without config.toml, got: %v", errs)
	}
}

// --- Cross-container ID validation tests ---

func TestValidateProject_ContainerID_MatchingConfig(t *testing.T) {
	// Case (a): two workflows share the same container id with identical configs — valid.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    id = "shared"
    image = "myimage:latest"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  container {
    id = "shared"
    image = "myimage:latest"
  }
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors for matching container configs, got: %v", errs)
	}
}

func TestValidateProject_ContainerID_OneFullOneIDOnly(t *testing.T) {
	// Case (b): one workflow has full config, the other only declares id — valid.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    id = "shared"
    image = "myimage:latest"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  container {
    id = "shared"
  }
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors for one full config + one id-only, got: %v", errs)
	}
}

func TestValidateProject_ContainerID_AllIDOnly(t *testing.T) {
	// Case (c): all workflows only declare id — valid.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    id = "shared"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  container {
    id = "shared"
  }
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors for all id-only, got: %v", errs)
	}
}

func TestValidateProject_ContainerID_ConflictingConfig(t *testing.T) {
	// Invalid: two workflows share the same container id with different configs.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    id = "shared"
    image = "image-a:latest"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  container {
    id = "shared"
    image = "image-b:latest"
  }
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "container id") && strings.Contains(e, "conflicts") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected container id conflict error, got: %v", errs)
	}
}

func TestValidateProject_ContainerID_DefaultSharedValid(t *testing.T) {
	// Multiple container workflows with no explicit id share _default — valid when one has config.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    image = "myimage:latest"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	if len(errs) > 0 {
		t.Errorf("expected no errors for default container id, got: %v", errs)
	}
}

func TestValidateProject_ContainerID_DefaultConflict(t *testing.T) {
	// Multiple container workflows with no explicit id, conflicting configs — invalid.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "a.cloche"), []byte(`workflow "dev" {
  container {
    image = "image-a:latest"
  }
  step code {
    run = "echo a"
    results = [success]
  }
  code:success -> done
}`), 0644)

	os.WriteFile(filepath.Join(clocheDir, "b.cloche"), []byte(`workflow "test" {
  container {
    image = "image-b:latest"
  }
  step run {
    run = "echo b"
    results = [success]
  }
  run:success -> done
}`), 0644)

	errs := validateProject(dir, "")
	found := false
	for _, e := range errs {
		if strings.Contains(e, "container id") && strings.Contains(e, "conflicts") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected container id conflict error for default id, got: %v", errs)
	}
}
