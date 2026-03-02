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
		filepath.Join("cloche", "develop.cloche"),
		filepath.Join("cloche", "Dockerfile"),
		filepath.Join("cloche", ".cloche", "prompts", "implement.md"),
		filepath.Join("cloche", ".cloche", "prompts", "fix.md"),
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
		}
	}

	data, _ := os.ReadFile(filepath.Join("cloche", "develop.cloche"))
	if !strings.Contains(string(data), `workflow "develop"`) {
		t.Errorf("workflow file missing workflow name")
	}
}

func TestCmdInit_CustomFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{"--workflow", "build", "--image", "python:3.12"})

	if _, err := os.Stat(filepath.Join("cloche", "build.cloche")); os.IsNotExist(err) {
		t.Error("expected cloche/build.cloche to exist")
	}
	if _, err := os.Stat(filepath.Join("cloche", "develop.cloche")); err == nil {
		t.Error("cloche/develop.cloche should not exist with --workflow build")
	}

	data, _ := os.ReadFile(filepath.Join("cloche", "Dockerfile"))
	if !strings.Contains(string(data), "FROM python:3.12") {
		t.Error("Dockerfile should contain custom base image")
	}

	data, _ = os.ReadFile(filepath.Join("cloche", "build.cloche"))
	if !strings.Contains(string(data), `workflow "build"`) {
		t.Error("workflow file should contain custom workflow name")
	}
}

func TestCmdInit_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.MkdirAll("cloche", 0755)
	os.WriteFile(filepath.Join("cloche", "Dockerfile"), []byte("custom"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join("cloche", "Dockerfile"))
	if string(data) != "custom" {
		t.Error("existing Dockerfile was overwritten")
	}

	if _, err := os.Stat(filepath.Join("cloche", "develop.cloche")); os.IsNotExist(err) {
		t.Error("cloche/develop.cloche should still be created")
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
	if !strings.Contains(content, "cloche/.cloche/*/") {
		t.Error(".gitignore should contain cloche/.cloche/*/")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should contain .gitworktrees/")
	}
}

func TestCmdInit_GitignoreNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile(".gitignore", []byte("cloche/.cloche/*/\n"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile(".gitignore")
	content := string(data)
	if strings.Count(content, "cloche/.cloche/*/") != 1 {
		t.Error(".gitignore should not duplicate existing entries")
	}
	if !strings.Contains(content, ".gitworktrees/") {
		t.Error(".gitignore should still add missing entries")
	}
}

func TestCmdInit_WorkflowTemplatePromptPaths(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmdInit([]string{})

	data, _ := os.ReadFile(filepath.Join("cloche", "develop.cloche"))
	content := string(data)
	if !strings.Contains(content, `file(".cloche/prompts/implement.md")`) {
		t.Error("workflow template should reference .cloche/prompts/implement.md")
	}
	if !strings.Contains(content, `file(".cloche/prompts/fix.md")`) {
		t.Error("workflow template should reference .cloche/prompts/fix.md")
	}
}
