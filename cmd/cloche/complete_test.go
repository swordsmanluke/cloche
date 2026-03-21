package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveCompletions_Subcommand verifies that completing position 1
// returns the subcommand list.
func TestResolveCompletions_Subcommand(t *testing.T) {
	completions := resolveCompletions(1, []string{"cloche", ""})
	if len(completions) == 0 {
		t.Fatal("expected subcommand completions, got none")
	}
	found := false
	for _, c := range completions {
		if c == "run" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'run' in subcommand completions, got %v", completions)
	}
}

// TestResolveCompletions_SubcommandPrefix verifies prefix filtering at position 1.
func TestResolveCompletions_SubcommandPrefix(t *testing.T) {
	completions := resolveCompletions(1, []string{"cloche", "st"})
	for _, c := range completions {
		if !strings.HasPrefix(c, "st") {
			t.Errorf("completion %q does not start with 'st'", c)
		}
	}
	// "status" and "stop" should be present.
	want := map[string]bool{"status": false, "stop": false}
	for _, c := range completions {
		want[c] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected %q in completions, got %v", name, completions)
		}
	}
}

// TestResolveCompletions_EmptyIndex returns all subcommands for index 0.
func TestResolveCompletions_EmptyIndex(t *testing.T) {
	completions := resolveCompletions(0, []string{"cloche"})
	if len(completions) == 0 {
		t.Fatal("expected completions for index 0")
	}
}

// TestStaticCompletions_Run verifies flag completions for "run".
func TestStaticCompletions_Run(t *testing.T) {
	completions := staticCompletions("run", 2, []string{"cloche", "run", ""}, "")
	hasWorkflow := false
	for _, c := range completions {
		if c == "--workflow" {
			hasWorkflow = true
		}
	}
	if !hasWorkflow {
		t.Errorf("expected --workflow in run completions, got %v", completions)
	}
}

// TestStaticCompletions_RunWorkflowValue verifies no flags are returned after --workflow.
func TestStaticCompletions_RunWorkflowValue(t *testing.T) {
	completions := staticCompletions("run", 3, []string{"cloche", "run", "--workflow", ""}, "")
	// Should return local workflow names (may be empty in test dir, which is fine).
	// Just verify --workflow is NOT in the results.
	for _, c := range completions {
		if c == "--workflow" {
			t.Errorf("--workflow should not appear after --workflow flag, got %v", completions)
		}
	}
}

// TestStaticCompletions_ListState verifies state value completions.
func TestStaticCompletions_ListState(t *testing.T) {
	completions := staticCompletions("list", 3, []string{"cloche", "list", "--state", ""}, "")
	want := []string{"running", "pending", "succeeded", "failed", "cancelled"}
	for _, w := range want {
		found := false
		for _, c := range completions {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected state value %q in completions, got %v", w, completions)
		}
	}
}

// TestStaticCompletions_LogsType verifies log type completions.
func TestStaticCompletions_LogsType(t *testing.T) {
	completions := staticCompletions("logs", 3, []string{"cloche", "logs", "--type", ""}, "")
	want := []string{"full", "script", "llm"}
	for _, w := range want {
		found := false
		for _, c := range completions {
			if c == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected log type %q in completions, got %v", w, completions)
		}
	}
}

// TestStaticCompletions_Loop verifies loop subcommand completions.
func TestStaticCompletions_Loop(t *testing.T) {
	completions := staticCompletions("loop", 2, []string{"cloche", "loop", ""}, "")
	hasStop := false
	hasResume := false
	for _, c := range completions {
		if c == "stop" {
			hasStop = true
		}
		if c == "resume" {
			hasResume = true
		}
	}
	if !hasStop || !hasResume {
		t.Errorf("expected stop and resume in loop completions, got %v", completions)
	}
}

// TestCompletionFilterPrefix verifies prefix filtering.
func TestCompletionFilterPrefix(t *testing.T) {
	items := []string{"run", "resume", "status", "stop"}
	got := completionFilterPrefix(items, "st")
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %v", got)
	}
	for _, c := range got {
		if !strings.HasPrefix(c, "st") {
			t.Errorf("unexpected completion %q", c)
		}
	}
}

// TestCompletionFilterPrefix_Empty returns all items when prefix is empty.
func TestCompletionFilterPrefix_Empty(t *testing.T) {
	items := []string{"a", "b", "c"}
	got := completionFilterPrefix(items, "")
	if len(got) != 3 {
		t.Errorf("expected all 3 items, got %v", got)
	}
}

// TestGenerateCompletionScripts_CreatesFiles verifies that both scripts are
// written to the target directory.
func TestGenerateCompletionScripts_CreatesFiles(t *testing.T) {
	dir := t.TempDir()

	// Redirect HOME so offerShellIntegration doesn't pollute the real ~/.zshrc.
	t.Setenv("HOME", t.TempDir())

	generateCompletionScripts(dir)

	bashPath := filepath.Join(dir, "cloche.bash")
	if _, err := os.Stat(bashPath); os.IsNotExist(err) {
		t.Errorf("expected %s to exist", bashPath)
	}

	zshPath := filepath.Join(dir, "cloche.zsh")
	if _, err := os.Stat(zshPath); os.IsNotExist(err) {
		t.Errorf("expected %s to exist", zshPath)
	}
}

// TestGenerateCompletionScripts_BashContent verifies bash script content.
func TestGenerateCompletionScripts_BashContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	generateCompletionScripts(dir)

	data, err := os.ReadFile(filepath.Join(dir, "cloche.bash"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "_cloche_complete") {
		t.Error("bash script should define _cloche_complete function")
	}
	if !strings.Contains(content, "complete -F _cloche_complete cloche") {
		t.Error("bash script should register completion function")
	}
	if !strings.Contains(content, "cloche complete") {
		t.Error("bash script should call 'cloche complete'")
	}
}

// TestGenerateCompletionScripts_ZshContent verifies zsh script content.
func TestGenerateCompletionScripts_ZshContent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	generateCompletionScripts(dir)

	data, err := os.ReadFile(filepath.Join(dir, "cloche.zsh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "_cloche()") {
		t.Error("zsh script should define _cloche function")
	}
	if !strings.Contains(content, "cloche complete") {
		t.Error("zsh script should call 'cloche complete'")
	}
	if !strings.Contains(content, "compadd") {
		t.Error("zsh script should use compadd")
	}
	if !strings.Contains(content, "compdef _cloche cloche") {
		t.Error("zsh script should register with compdef")
	}
}

// TestGenerateCompletionScripts_WorkflowNames verifies workflow name completions
// when .cloche/ directory is present.
func TestGenerateCompletionScripts_WorkflowNames(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Create a .cloche dir with a workflow file.
	os.MkdirAll(".cloche", 0755)
	os.WriteFile(".cloche/develop.cloche", []byte(`workflow "develop" {
  step code {
    prompt = "do it"
    results = [success, fail]
  }
  code:success -> done
  code:fail -> abort
}
`), 0644)

	completions := resolveCompletions(3, []string{"cloche", "run", "--workflow", ""})
	found := false
	for _, c := range completions {
		if c == "develop" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'develop' in workflow completions, got %v", completions)
	}
}

// TestAlreadyInFile verifies the duplicate-detection helper.
func TestAlreadyInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")

	os.WriteFile(path, []byte("some content here\n"), 0644)

	if !alreadyInFile(path, "some content") {
		t.Error("expected alreadyInFile to return true for existing content")
	}
	if alreadyInFile(path, "not present") {
		t.Error("expected alreadyInFile to return false for absent content")
	}
	if alreadyInFile(filepath.Join(dir, "nonexistent"), "anything") {
		t.Error("expected alreadyInFile to return false for missing file")
	}
}
