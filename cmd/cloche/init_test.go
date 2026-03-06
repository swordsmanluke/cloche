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
		filepath.Join(".cloche", "prompts", "fix.md"),
		filepath.Join(".cloche", "prompts", "update-docs.md"),
		filepath.Join(".cloche", "config.toml"),
		filepath.Join(".cloche", "version"),
		filepath.Join(".cloche", "host.cloche"),
		filepath.Join(".cloche", "scripts", "prepare-prompt.sh"),
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

	// Verify prepare-prompt.sh is executable
	info, err := os.Stat(filepath.Join(".cloche", "scripts", "prepare-prompt.sh"))
	if err != nil {
		t.Fatal("expected prepare-prompt.sh to exist")
	}
	if info.Mode()&0111 == 0 {
		t.Error("expected prepare-prompt.sh to be executable")
	}
}

func TestCmdInit_CustomFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--workflow", "build", "--image", "python:3.12"})

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
	if !strings.Contains(content, ".cloche/*-*-*/") {
		t.Error(".gitignore should contain .cloche/*-*-*/")
	}
	if !strings.Contains(content, ".cloche/run-*/") {
		t.Error(".gitignore should contain .cloche/run-*/")
	}
	if !strings.Contains(content, ".cloche/attempt_count/") {
		t.Error(".gitignore should contain .cloche/attempt_count/")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should contain .gitworktrees/")
	}
	// Old entries should not be present
	if strings.Contains(content, ".cloche/*/") {
		t.Error(".gitignore should not contain old .cloche/*/ entry")
	}
}

func TestCmdInit_GitignoreNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile(".gitignore", []byte(".cloche/*-*-*/\n"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(".gitignore")
	content := string(data)
	if strings.Count(content, ".cloche/*-*-*/") != 1 {
		t.Error(".gitignore should not duplicate existing entries")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should still add missing entries")
	}
}

func TestCmdInit_GitignoreRemovesOldEntries(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile(".gitignore", []byte(".cloche/*/\n!.cloche/prompts/\n!.cloche/overrides/\n!.cloche/evolution/\n.gitworktrees/\n"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(".gitignore")
	content := string(data)

	// Old entries should be removed
	for _, old := range []string{".cloche/*/", "!.cloche/prompts/", "!.cloche/overrides/", "!.cloche/evolution/"} {
		if strings.Contains(content, old) {
			t.Errorf(".gitignore should not contain old entry %q", old)
		}
	}

	// New entries should be present
	for _, entry := range []string{".cloche/*-*-*/", ".cloche/run-*/", ".cloche/attempt_count/"} {
		if !strings.Contains(content, entry) {
			t.Errorf(".gitignore should contain new entry %q", entry)
		}
	}

	// .gitworktrees/ should still be present (not duplicated)
	if strings.Count(content, ".gitworktrees/") != 1 {
		t.Error(".gitworktrees/ should appear exactly once")
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

func TestCmdInit_PreparePromptExecutable(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	info, err := os.Stat(filepath.Join(".cloche", "scripts", "prepare-prompt.sh"))
	if err != nil {
		t.Fatal("expected prepare-prompt.sh to exist")
	}
	if info.Mode()&0111 == 0 {
		t.Error("expected prepare-prompt.sh to have executable permission bits")
	}
}

func TestCmdInit_WorkflowTemplatePromptPaths(t *testing.T) {
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
	if !strings.Contains(content, `file(".cloche/prompts/fix.md")`) {
		t.Error("workflow template should reference .cloche/prompts/fix.md")
	}
	if !strings.Contains(content, `file(".cloche/prompts/update-docs.md")`) {
		t.Error("workflow template should reference .cloche/prompts/update-docs.md")
	}
}
