package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMockLLMScript creates a shell script at a temp path that outputs the given content
// to stdout, ignoring all arguments and stdin.
func writeMockLLMScript(t *testing.T, output string) string {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "llm-output.txt")
	if err := os.WriteFile(outFile, []byte(output), 0644); err != nil {
		t.Fatalf("write mock output: %v", err)
	}
	scriptFile := filepath.Join(t.TempDir(), "mock-llm")
	script := "#!/bin/sh\nexec cat '" + strings.ReplaceAll(outFile, "'", "'\\''") + "'\n"
	if err := os.WriteFile(scriptFile, []byte(script), 0755); err != nil {
		t.Fatalf("write mock script: %v", err)
	}
	return scriptFile
}

func TestParseInitResponse_ParsesFencedBlock(t *testing.T) {
	response := "Here are the updated files:\n\n" +
		"```.cloche/Dockerfile\n" +
		"FROM cloche-agent:latest\nUSER root\n\nRUN apt-get install -y python3\n\nUSER agent\n" +
		"```\n"
	result := parseInitResponse(response)
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(result), result)
	}
	content, ok := result[".cloche/Dockerfile"]
	if !ok {
		t.Fatal("expected .cloche/Dockerfile in result")
	}
	if !strings.Contains(content, "python3") {
		t.Error("expected content to contain 'python3'")
	}
	if strings.Contains(content, "TODO(cloche-init)") {
		t.Error("content should not contain TODO(cloche-init)")
	}
}

func TestParseInitResponse_MultipleFiles(t *testing.T) {
	response := "```.cloche/Dockerfile\nFROM cloche-agent:latest\nUSER agent\n```\n\n" +
		"```.cloche/develop.cloche\nworkflow \"develop\" {}\n```\n"
	result := parseInitResponse(response)
	if len(result) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(result), result)
	}
	if _, ok := result[".cloche/Dockerfile"]; !ok {
		t.Error("expected .cloche/Dockerfile in result")
	}
	if _, ok := result[".cloche/develop.cloche"]; !ok {
		t.Error("expected .cloche/develop.cloche in result")
	}
}

func TestParseInitResponse_SkipsLanguageTags(t *testing.T) {
	// Language tags like "go", "python", "dockerfile" should not be treated as file paths
	response := "```go\nfunc main() {}\n```\n\n```python\nprint('hi')\n```\n"
	result := parseInitResponse(response)
	if len(result) != 0 {
		t.Errorf("expected no files parsed from language tags, got %d: %v", len(result), result)
	}
}

func TestParseInitResponse_EmptyResponse(t *testing.T) {
	result := parseInitResponse("")
	if len(result) != 0 {
		t.Errorf("expected empty result for empty response, got %d", len(result))
	}
}

func TestParseInitResponse_ContentEndsWithNewline(t *testing.T) {
	response := "```.cloche/Dockerfile\nFROM cloche-agent:latest\n```\n"
	result := parseInitResponse(response)
	content := result[".cloche/Dockerfile"]
	if !strings.HasSuffix(content, "\n") {
		t.Error("content should end with newline")
	}
}

func TestParseInitResponse_NestedPathWithDots(t *testing.T) {
	response := "```.cloche/prompts/implement.md\n# Project\n\nLanguage: Go\n```\n"
	result := parseInitResponse(response)
	if _, ok := result[".cloche/prompts/implement.md"]; !ok {
		t.Error("expected .cloche/prompts/implement.md in result")
	}
}

func TestResolveLLMCommand_ExplicitArg(t *testing.T) {
	t.Setenv("CLOCHE_AGENT_COMMAND", "other-llm")
	cmd, ok := resolveLLMCommand("explicit-cmd")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cmd != "explicit-cmd" {
		t.Errorf("expected 'explicit-cmd', got %q", cmd)
	}
}

func TestResolveLLMCommand_EnvVar(t *testing.T) {
	t.Setenv("CLOCHE_AGENT_COMMAND", "env-llm")
	cmd, ok := resolveLLMCommand("")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cmd != "env-llm" {
		t.Errorf("expected 'env-llm', got %q", cmd)
	}
}

func TestResolveLLMCommand_NoneAvailable(t *testing.T) {
	// Unset env var and use a HOME that has no global config
	t.Setenv("CLOCHE_AGENT_COMMAND", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Only returns true if 'claude' is on PATH; skip asserting false since
	// the developer environment may have it
	_, _ = resolveLLMCommand("")
}

func TestRunLLMInitPhase_UpdatesFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Scaffold without LLM first
	cmdInit([]string{"--new", "--no-llm"})

	// Verify placeholder is present before LLM run
	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if !strings.Contains(string(data), "TODO(cloche-init)") {
		t.Fatal("Dockerfile should have TODO(cloche-init) before LLM run")
	}

	// Build mock LLM response that replaces the Dockerfile placeholder
	mockDockerfile := "FROM cloche-agent:latest\nUSER root\n\nRUN apt-get update && apt-get install -y golang-go\n\nUSER agent\n"
	mockResponse := "```.cloche/Dockerfile\n" + mockDockerfile + "```\n"
	mockCmd := writeMockLLMScript(t, mockResponse)

	runLLMInitPhase(mockCmd, "develop")

	updated, err := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if err != nil {
		t.Fatalf("Dockerfile missing after LLM phase: %v", err)
	}
	if strings.Contains(string(updated), "TODO(cloche-init)") {
		t.Error("Dockerfile should no longer contain TODO(cloche-init) after LLM update")
	}
	if !strings.Contains(string(updated), "golang-go") {
		t.Error("Dockerfile should contain LLM-generated content 'golang-go'")
	}
}

func TestRunLLMInitPhase_NonFatalOnFailure(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--new", "--no-llm"})

	origDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))

	// Use a command that will fail (non-existent binary)
	runLLMInitPhase("/nonexistent/command/that/does/not/exist", "develop")

	// Files should be unchanged
	afterDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if string(origDockerfile) != string(afterDockerfile) {
		t.Error("Dockerfile should be unchanged after LLM failure")
	}
	if !strings.Contains(string(afterDockerfile), "TODO(cloche-init)") {
		t.Error("Dockerfile should still contain TODO(cloche-init) placeholder after LLM failure")
	}
}

func TestRunLLMInitPhase_NonFatalOnEmptyResponse(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--new", "--no-llm"})

	origDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))

	// Mock command that outputs nothing
	emptyCmd := writeMockLLMScript(t, "")
	runLLMInitPhase(emptyCmd, "develop")

	afterDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if string(origDockerfile) != string(afterDockerfile) {
		t.Error("Dockerfile should be unchanged after empty LLM response")
	}
}

func TestRunLLMInitPhase_NonFatalOnUnparsableResponse(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--new", "--no-llm"})

	origDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))

	// Mock command that outputs text without any parseable file blocks
	garbledCmd := writeMockLLMScript(t, "I don't know how to fill this in. Sorry!")
	runLLMInitPhase(garbledCmd, "develop")

	afterDockerfile, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if string(origDockerfile) != string(afterDockerfile) {
		t.Error("Dockerfile should be unchanged after unparseable LLM response")
	}
}

func TestRunLLMInitPhase_OnlyWritesAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--new", "--no-llm"})

	// Mock LLM that tries to write a disallowed path
	mockResponse := "```evil/path/../../etc/passwd\nrooted!\n```\n\n" +
		"```.cloche/Dockerfile\nFROM cloche-agent:latest\nUSER agent\n```\n"
	mockCmd := writeMockLLMScript(t, mockResponse)

	runLLMInitPhase(mockCmd, "develop")

	// The evil path should not have been written
	if _, err := os.Stat(filepath.Join(dir, "evil", "path")); err == nil {
		t.Error("LLM should not be allowed to write arbitrary paths")
	}
	// The Dockerfile should still have been updated (it's in the allowlist)
	updated, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if strings.Contains(string(updated), "TODO(cloche-init)") {
		t.Error("Dockerfile should have been updated by LLM")
	}
}

func TestCmdInit_NoLLMFlag_SkipsLLMPhase(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Even with a valid agent command, --no-llm should skip the phase entirely
	// (the mock would update Dockerfile if invoked)
	mockResponse := "```.cloche/Dockerfile\nFROM cloche-agent:latest\nUSER agent\n```\n"
	mockCmd := writeMockLLMScript(t, mockResponse)

	cmdInit([]string{"--new", "--no-llm", "--agent-command", mockCmd})

	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if !strings.Contains(string(data), "TODO(cloche-init)") {
		t.Error("Dockerfile should still contain TODO(cloche-init) when --no-llm is set")
	}
}

func TestCmdInit_AgentCommandFlag_UsedForLLM(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	mockDockerfile := "FROM cloche-agent:latest\nUSER root\n\nRUN apt-get install -y nodejs\n\nUSER agent\n"
	mockResponse := "```.cloche/Dockerfile\n" + mockDockerfile + "```\n"
	mockCmd := writeMockLLMScript(t, mockResponse)

	cmdInit([]string{"--new", "--agent-command", mockCmd})

	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if !strings.Contains(string(data), "nodejs") {
		t.Error("Dockerfile should contain LLM-generated content when --agent-command is used")
	}
}

func TestRunLLMInitPhase_CustomWorkflow(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--new", "--no-llm", "--workflow", "build"})

	// Verify the custom workflow file has the placeholder
	data, _ := os.ReadFile(filepath.Join(".cloche", "build.cloche"))
	if !strings.Contains(string(data), "TODO(cloche-init)") {
		t.Fatal("build.cloche should have TODO(cloche-init) before LLM run")
	}

	updatedWorkflow := `workflow "build" {
  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }
  step test {
    run     = "make test"
    results = [success, fail]
  }
  implement:success -> test
  test:success -> done
  test:fail -> abort
}
`
	mockResponse := "```.cloche/build.cloche\n" + updatedWorkflow + "```\n"
	mockCmd := writeMockLLMScript(t, mockResponse)

	runLLMInitPhase(mockCmd, "build")

	updated, _ := os.ReadFile(filepath.Join(".cloche", "build.cloche"))
	if strings.Contains(string(updated), "TODO(cloche-init)") {
		t.Error("build.cloche should not contain TODO(cloche-init) after LLM update")
	}
	if !strings.Contains(string(updated), "make test") {
		t.Error("build.cloche should contain LLM-generated test command")
	}
}

func TestBuildInitPrompt_ContainsProjectContext(t *testing.T) {
	projectCtx := "## Project root contents\n\n  go.mod\n\n## Key project files\n\n### go.mod\n\n```\nmodule example\n```\n\n"
	templates := map[string]string{
		".cloche/Dockerfile": "FROM cloche-agent:latest\n# TODO(cloche-init): install deps\nUSER agent\n",
	}
	paths := []string{".cloche/Dockerfile"}

	prompt := buildInitPrompt(projectCtx, paths, templates)

	if !strings.Contains(prompt, "go.mod") {
		t.Error("prompt should contain project context with go.mod")
	}
	if !strings.Contains(prompt, "TODO(cloche-init)") {
		t.Error("prompt should contain template with TODO(cloche-init) placeholder")
	}
	if !strings.Contains(prompt, "Fill in the TODO(cloche-init)") {
		t.Error("prompt should contain instructions to fill in placeholders")
	}
}

func TestCollectProjectContext_IncludesKeyFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module example.com/myproject\n\ngo 1.21\n"), 0644)
	os.WriteFile("Makefile", []byte("test:\n\tgo test ./...\n"), 0644)

	ctx := collectProjectContext()

	if !strings.Contains(ctx, "go.mod") {
		t.Error("context should mention go.mod in file listing")
	}
	if !strings.Contains(ctx, "module example.com/myproject") {
		t.Error("context should include go.mod contents")
	}
	if !strings.Contains(ctx, "go test ./...") {
		t.Error("context should include Makefile contents")
	}
}

func TestCollectProjectContext_ExcludesNodeModules(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.MkdirAll("node_modules/some-package", 0755)
	os.WriteFile("package.json", []byte(`{"name":"myapp","scripts":{"test":"jest"}}`), 0644)

	ctx := collectProjectContext()

	if strings.Contains(ctx, "node_modules") {
		t.Error("context should not include node_modules directory")
	}
	if !strings.Contains(ctx, "jest") {
		t.Error("context should include package.json contents")
	}
}
