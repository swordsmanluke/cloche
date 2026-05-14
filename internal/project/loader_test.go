package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/project"
)

func setup(t *testing.T, configContent string) string {
	t.Helper()
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0755); err != nil {
		t.Fatalf("mkdir .cloche: %v", err)
	}
	if configContent != "" {
		if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(configContent), 0644); err != nil {
			t.Fatalf("write config.toml: %v", err)
		}
	}
	return dir
}

func TestLoad_NoConfigFile(t *testing.T) {
	dir := setup(t, "")

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if proj.Dir != dir {
		t.Errorf("Dir: got %q, want %q", proj.Dir, dir)
	}
	if len(proj.Repositories) != 0 {
		t.Errorf("expected 0 repositories, got %d", len(proj.Repositories))
	}
}

func TestLoad_ConfigWithRepositories(t *testing.T) {
	dir := setup(t, `
[[repositories]]
name = "backend"
path = "./repos/backend"

[[repositories]]
name = "frontend"
path = "./repos/frontend"
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(proj.Repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(proj.Repositories))
	}

	backend := proj.Repositories[0]
	if backend.Name != "backend" {
		t.Errorf("repo[0].Name: got %q, want %q", backend.Name, "backend")
	}
	// Path is stored as declared in config.toml — relative to project root, not .cloche/.
	if backend.Path != "./repos/backend" {
		t.Errorf("repo[0].Path: got %q, want %q", backend.Path, "./repos/backend")
	}

	frontend := proj.Repositories[1]
	if frontend.Name != "frontend" {
		t.Errorf("repo[1].Name: got %q, want %q", frontend.Name, "frontend")
	}
	if frontend.Path != "./repos/frontend" {
		t.Errorf("repo[1].Path: got %q, want %q", frontend.Path, "./repos/frontend")
	}
}

func TestLoad_SingleEntryImplicitDefault(t *testing.T) {
	dir := setup(t, `
[[repositories]]
name = "main"
path = "./repos/main"
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(proj.Repositories) != 1 {
		t.Fatalf("expected 1 repository, got %d", len(proj.Repositories))
	}

	def := domain.DefaultRepository(proj.Repositories)
	if def == nil {
		t.Fatal("DefaultRepository: expected non-nil for single-entry project")
	}
	if def.Name != "main" {
		t.Errorf("DefaultRepository.Name: got %q, want %q", def.Name, "main")
	}
}

func TestLoad_MultipleEntries_NoImplicitDefault(t *testing.T) {
	dir := setup(t, `
[[repositories]]
name = "backend"
path = "./repos/backend"

[[repositories]]
name = "frontend"
path = "./repos/frontend"
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	def := domain.DefaultRepository(proj.Repositories)
	if def != nil {
		t.Errorf("DefaultRepository: expected nil for multi-entry project, got %q", def.Name)
	}
}

func TestLoad_PathsRelativeToProjectRoot(t *testing.T) {
	// Verifies that paths in config.toml are stored as-is (relative to project root,
	// not re-rooted to .cloche/).
	dir := setup(t, `
[[repositories]]
name = "sibling"
path = "../sibling-repo"
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proj.Repositories) != 1 {
		t.Fatalf("expected 1 repository, got %d", len(proj.Repositories))
	}
	// Path must be the original string from config.toml, not prefixed with .cloche/.
	if proj.Repositories[0].Path != "../sibling-repo" {
		t.Errorf("Path: got %q, want %q", proj.Repositories[0].Path, "../sibling-repo")
	}
}

func TestLoad_MalformedConfig(t *testing.T) {
	dir := setup(t, `this is not valid toml ][[[`)

	_, err := project.Load(dir)
	if err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
	if !strings.Contains(err.Error(), "project.Load") {
		t.Errorf("error should be wrapped with project.Load context: %v", err)
	}
}
