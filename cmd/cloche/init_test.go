package main

import (
	"os"
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
		"develop.cloche",
		"Dockerfile",
		"prompts/implement.md",
		"prompts/fix.md",
	} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist", path)
		}
	}

	data, _ := os.ReadFile("develop.cloche")
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

	if _, err := os.Stat("build.cloche"); os.IsNotExist(err) {
		t.Error("expected build.cloche to exist")
	}
	if _, err := os.Stat("develop.cloche"); err == nil {
		t.Error("develop.cloche should not exist with --workflow build")
	}

	data, _ := os.ReadFile("Dockerfile")
	if !strings.Contains(string(data), "FROM python:3.12") {
		t.Error("Dockerfile should contain custom base image")
	}

	data, _ = os.ReadFile("build.cloche")
	if !strings.Contains(string(data), `workflow "build"`) {
		t.Error("workflow file should contain custom workflow name")
	}
}

func TestCmdInit_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("Dockerfile", []byte("custom"), 0644)

	cmdInit([]string{})

	data, _ := os.ReadFile("Dockerfile")
	if string(data) != "custom" {
		t.Error("existing Dockerfile was overwritten")
	}

	if _, err := os.Stat("develop.cloche"); os.IsNotExist(err) {
		t.Error("develop.cloche should still be created")
	}
}
