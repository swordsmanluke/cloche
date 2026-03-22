package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdInit_DefaultFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	for _, path := range []string{
		filepath.Join(".cloche", "develop.cloche"),
		filepath.Join(".cloche", "Dockerfile"),
		filepath.Join(".cloche", "prompts", "implement.md"),
		filepath.Join(".cloche", "prompts", "fix-tests.md"),
		filepath.Join(".cloche", "prompts", "fix-merge.md"),
		filepath.Join(".cloche", "config.toml"),
		filepath.Join(".cloche", "version"),
		filepath.Join(".cloche", "host.cloche"),
		filepath.Join(".cloche", "scripts", "get-tasks.py"),
		filepath.Join(".cloche", "scripts", "claim-task.py"),
		filepath.Join(".cloche", "scripts", "prepare-merge.py"),
		filepath.Join(".cloche", "scripts", "merge.py"),
		filepath.Join(".cloche", "scripts", "release-task.py"),
		filepath.Join(".cloche", "scripts", "cleanup.py"),
		filepath.Join(".cloche", "scripts", "unclaim.py"),
		".clocheignore",
		filepath.Join(".cloche", "task_list.json"),
		filepath.Join("test", "cloche", "test_cloche.py"),
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
		}
	}

	data, _ := os.ReadFile(filepath.Join(".cloche", "develop.cloche"))
	if !strings.Contains(string(data), `workflow "develop"`) {
		t.Errorf("workflow file missing workflow name")
	}

	// Verify overrides directory was created
	if info, err := os.Stat(filepath.Join(".cloche", "overrides")); err != nil || !info.IsDir() {
		t.Error("expected .cloche/overrides/ directory to exist")
	}

	// Verify scripts directory was created
	if info, err := os.Stat(filepath.Join(".cloche", "scripts")); err != nil || !info.IsDir() {
		t.Error("expected .cloche/scripts/ directory to exist")
	}

	// Verify test/cloche/ directory was created
	if info, err := os.Stat(filepath.Join("test", "cloche")); err != nil || !info.IsDir() {
		t.Error("expected test/cloche/ directory to exist")
	}

	// Old v1 files should not be created
	for _, path := range []string{
		filepath.Join(".cloche", "scripts", "prepare-prompt.sh"),
		filepath.Join(".cloche", "prompts", "fix.md"),
		filepath.Join(".cloche", "prompts", "update-docs.md"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected %s to NOT exist", path)
		}
	}
}

func TestCmdInit_CustomFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--workflow", "build", "--base-image", "python:3.12"})

	if _, err := os.Stat(filepath.Join(".cloche", "build.cloche")); os.IsNotExist(err) {
		t.Error("expected .cloche/build.cloche to exist")
	}
	if _, err := os.Stat(filepath.Join(".cloche", "develop.cloche")); err == nil {
		t.Error(".cloche/develop.cloche should not exist with --workflow build")
	}

	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if !strings.Contains(string(data), "FROM python:3.12") {
		t.Error("Dockerfile should contain custom base image")
	}

	data, _ = os.ReadFile(filepath.Join(".cloche", "build.cloche"))
	if !strings.Contains(string(data), `workflow "build"`) {
		t.Error("workflow file should contain custom workflow name")
	}
}

func TestCmdInit_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.MkdirAll(".cloche", 0755)
	os.WriteFile(filepath.Join(".cloche", "Dockerfile"), []byte("custom"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	if string(data) != "custom" {
		t.Error("existing Dockerfile was overwritten")
	}

	if _, err := os.Stat(filepath.Join(".cloche", "develop.cloche")); os.IsNotExist(err) {
		t.Error(".cloche/develop.cloche should still be created")
	}
}

func TestCmdInit_GitignoreEntries(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatalf("expected .gitignore to exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".cloche/logs/") {
		t.Error(".gitignore should contain .cloche/logs/")
	}
	if !strings.Contains(content, ".cloche/runs/") {
		t.Error(".gitignore should contain .cloche/runs/")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should contain .gitworktrees/")
	}
	if !strings.Contains(content, ".cloche/task_list.json") {
		t.Error(".gitignore should contain .cloche/task_list.json")
	}
	// Old v1 entries should not be present
	for _, old := range []string{".cloche/*-*-*/", ".cloche/run-*/", ".cloche/attempt_count/"} {
		if strings.Contains(content, old) {
			t.Errorf(".gitignore should not contain old entry %q", old)
		}
	}
}

func TestCmdInit_GitignoreNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile(".gitignore", []byte(".cloche/logs/\n"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(".gitignore")
	content := string(data)
	if strings.Count(content, ".cloche/logs/") != 1 {
		t.Error(".gitignore should not duplicate existing entries")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should still add missing entries")
	}
}

func TestCmdInit_DockerfileDefaultBaseImage(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "Dockerfile"))
	content := string(data)
	if !strings.Contains(content, "FROM cloche-agent:latest") {
		t.Error("Dockerfile should default to cloche-agent:latest base image")
	}
	if !strings.Contains(content, "python3") {
		t.Error("Dockerfile should install python3")
	}
}

func TestCmdInit_ConfigTOMLOrchestrationSection(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "config.toml"))
	content := string(data)
	if !strings.Contains(content, "[orchestration]") {
		t.Error("config.toml should contain commented [orchestration] section")
	}
	if !strings.Contains(content, "concurrency") {
		t.Error("config.toml should contain concurrency setting")
	}
	if !strings.Contains(content, "[daemon]") {
		t.Error("config.toml should contain [daemon] section")
	}
	// Image name should be based on directory basename
	dirName := filepath.Base(dir)
	expectedImage := dirName + "-cloche-agent:latest"
	if !strings.Contains(content, expectedImage) {
		t.Errorf("config.toml should contain project-specific image %q, got:\n%s", expectedImage, content)
	}
}

func TestCmdInit_ClocheignoreV2Patterns(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(".clocheignore")
	content := string(data)
	if !strings.Contains(content, ".cloche/logs/") {
		t.Error(".clocheignore should contain .cloche/logs/")
	}
	if !strings.Contains(content, ".cloche/runs/") {
		t.Error(".clocheignore should contain .cloche/runs/")
	}
	// Old v1 patterns should not be present
	for _, old := range []string{".cloche/*-*-*/", ".cloche/run-*/", ".cloche/attempt_count/"} {
		if strings.Contains(content, old) {
			t.Errorf(".clocheignore should not contain old pattern %q", old)
		}
	}
}

func TestCmdInit_VersionContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, err := os.ReadFile(filepath.Join(".cloche", "version"))
	if err != nil {
		t.Fatal("expected .cloche/version to exist")
	}
	if string(data) != "1\n" {
		t.Errorf("expected version content %q, got %q", "1\n", string(data))
	}
}

func TestCmdInit_ScriptsExecutable(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	for _, path := range []string{
		filepath.Join(".cloche", "scripts", "get-tasks.py"),
		filepath.Join(".cloche", "scripts", "claim-task.py"),
		filepath.Join(".cloche", "scripts", "prepare-merge.py"),
		filepath.Join(".cloche", "scripts", "merge.py"),
		filepath.Join(".cloche", "scripts", "release-task.py"),
		filepath.Join(".cloche", "scripts", "cleanup.py"),
		filepath.Join(".cloche", "scripts", "unclaim.py"),
	} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("expected %s to be executable", path)
		}
	}
}

func TestCmdInit_MergeCleanupScripts(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	for _, path := range []string{
		filepath.Join(".cloche", "scripts", "prepare-merge.py"),
		filepath.Join(".cloche", "scripts", "merge.py"),
		filepath.Join(".cloche", "scripts", "cleanup.py"),
	} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
			continue
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("expected %s to be executable", path)
		}
	}

	// Verify fix-merge.md prompt exists
	if _, err := os.Stat(filepath.Join(".cloche", "prompts", "fix-merge.md")); os.IsNotExist(err) {
		t.Error("expected .cloche/prompts/fix-merge.md to exist")
	}
}

func TestCmdInit_PrepareMergeContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "scripts", "prepare-merge.py"))
	content := string(data)
	if !strings.Contains(content, "child_run_id") {
		t.Error("prepare-merge.py should retrieve child_run_id from run context")
	}
	if !strings.Contains(content, "worktree_path") {
		t.Error("prepare-merge.py should store worktree_path via cloche set")
	}
	if !strings.Contains(content, "rebase") {
		t.Error("prepare-merge.py should perform a rebase")
	}
}

func TestCmdInit_MergeContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "scripts", "merge.py"))
	content := string(data)
	if !strings.Contains(content, "worktree_path") {
		t.Error("merge.py should retrieve worktree_path")
	}
	if !strings.Contains(content, "ff-only") {
		t.Error("merge.py should fast-forward merge")
	}
	if !strings.Contains(content, `"branch", "-D"`) {
		t.Error("merge.py should delete the feature branch")
	}
}

func TestCmdInit_CleanupContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "scripts", "cleanup.py"))
	content := string(data)
	if !strings.Contains(content, "child_run_id") {
		t.Error("cleanup.py should retrieve child_run_id from run context")
	}
	if !strings.Contains(content, `"worktree", "remove"`) {
		t.Error("cleanup.py should remove the worktree")
	}
	if !strings.Contains(content, `"branch", "-D"`) {
		t.Error("cleanup.py should delete the branch")
	}
}

func TestCmdInit_FixMergePromptContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "prompts", "fix-merge.md"))
	content := string(data)
	if !strings.Contains(content, "worktree_path") {
		t.Error("fix-merge.md should reference worktree_path")
	}
	if !strings.Contains(content, "rebase --continue") {
		t.Error("fix-merge.md should instruct running rebase --continue")
	}
}

func TestCmdInit_WorkflowTemplateV2(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "develop.cloche"))
	content := string(data)
	if !strings.Contains(content, `file(".cloche/prompts/implement.md")`) {
		t.Error("workflow template should reference .cloche/prompts/implement.md")
	}
	if !strings.Contains(content, `file(".cloche/prompts/fix-tests.md")`) {
		t.Error("workflow template should reference .cloche/prompts/fix-tests.md")
	}
	if !strings.Contains(content, "step commit") {
		t.Error("workflow template should have a commit step")
	}
	if !strings.Contains(content, "step test") {
		t.Error("workflow template should have a test step")
	}
	if !strings.Contains(content, "step fix-tests") {
		t.Error("workflow template should have a fix-tests step")
	}
	// Old v1 steps should not be present
	if strings.Contains(content, "step update-docs") {
		t.Error("workflow template should not have update-docs step")
	}
}

func TestCmdInit_HostWorkflowV2(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "host.cloche"))
	content := string(data)
	if !strings.Contains(content, `workflow "list-tasks"`) {
		t.Error("host.cloche should contain list-tasks workflow")
	}
	if !strings.Contains(content, `workflow "main"`) {
		t.Error("host.cloche should contain main workflow")
	}
	if !strings.Contains(content, `workflow "finalize"`) {
		t.Error("host.cloche should contain finalize workflow")
	}
	if !strings.Contains(content, "get-tasks.py") {
		t.Error("host.cloche should reference get-tasks.py")
	}
	if !strings.Contains(content, "claim-task.py") {
		t.Error("host.cloche should reference claim-task.py")
	}
	if !strings.Contains(content, "unclaim.py") {
		t.Error("host.cloche should reference unclaim.py")
	}
	// Old v1 script should not be present
	if strings.Contains(content, "prepare-prompt.sh") {
		t.Error("host.cloche should not reference prepare-prompt.sh")
	}
}

func TestCmdInit_TaskListJSON(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, err := os.ReadFile(filepath.Join(".cloche", "task_list.json"))
	if err != nil {
		t.Fatal("expected .cloche/task_list.json to exist")
	}
	content := string(data)
	if !strings.Contains(content, "Validate Agent works") {
		t.Error(".cloche/task_list.json should contain validation task")
	}
	if !strings.Contains(content, "Clean up cloche test files") {
		t.Error(".cloche/task_list.json should contain cleanup task")
	}
}

func TestCmdInit_TestClocheScript(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, err := os.ReadFile(filepath.Join("test", "cloche", "test_cloche.py"))
	if err != nil {
		t.Fatal("expected test/cloche/test_cloche.py to exist")
	}
	content := string(data)
	if !strings.Contains(content, "TestAgentSetup") {
		t.Error("test_cloche.py should contain TestAgentSetup class")
	}
	if !strings.Contains(content, "agent_test") {
		t.Error("test_cloche.py should reference agent_test file")
	}
	if !strings.Contains(content, "I exist!") {
		t.Error("test_cloche.py should check for 'I exist!' content")
	}
}

func TestCmdInit_GetTasksContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "scripts", "get-tasks.py"))
	content := string(data)
	if !strings.Contains(content, "task_list.json") {
		t.Error("get-tasks.py should reference task_list.json")
	}
	if !strings.Contains(content, "CLOCHE_STEP_OUTPUT") {
		t.Error("get-tasks.py should write to CLOCHE_STEP_OUTPUT")
	}
	if !strings.Contains(content, `"status": "open"`) || !strings.Contains(content, `.get("status") == "open"`) {
		// Either format is acceptable
		if !strings.Contains(content, "open") {
			t.Error("get-tasks.py should filter for open tasks")
		}
	}
}

func TestCmdInit_UnclaimContent(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join(".cloche", "scripts", "unclaim.py"))
	content := string(data)
	if !strings.Contains(content, "loop") {
		t.Error("unclaim.py should stop the loop")
	}
	if !strings.Contains(content, "open") {
		t.Error("unclaim.py should reset task to open")
	}
}
