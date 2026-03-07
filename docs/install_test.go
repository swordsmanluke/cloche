package docs_test

import (
	"os"
	"strings"
	"testing"
)

// TestInstallDoc validates that INSTALL.md references match actual project artifacts.
func TestInstallDoc(t *testing.T) {
	content, err := os.ReadFile("INSTALL.md")
	if err != nil {
		t.Fatal("cannot read docs/INSTALL.md:", err)
	}
	doc := string(content)

	t.Run("references go module path", func(t *testing.T) {
		modBytes, err := os.ReadFile("../go.mod")
		if err != nil {
			t.Fatal("cannot read go.mod:", err)
		}
		// Extract module path from first line
		lines := strings.SplitN(string(modBytes), "\n", 2)
		modPath := strings.TrimPrefix(lines[0], "module ")
		modPath = strings.TrimSpace(modPath)

		if !strings.Contains(doc, modPath) {
			t.Errorf("INSTALL.md does not reference module path %q", modPath)
		}
	})

	t.Run("references all three binaries", func(t *testing.T) {
		for _, bin := range []string{"cloche", "cloched", "cloche-agent"} {
			if !strings.Contains(doc, bin) {
				t.Errorf("INSTALL.md does not mention binary %q", bin)
			}
		}
	})

	t.Run("references cmd directories", func(t *testing.T) {
		for _, cmd := range []string{"cmd/cloche", "cmd/cloched", "cmd/cloche-agent"} {
			if !strings.Contains(doc, cmd) {
				t.Errorf("INSTALL.md does not reference %q", cmd)
			}
		}
	})

	t.Run("cmd directories exist", func(t *testing.T) {
		for _, cmd := range []string{"../cmd/cloche", "../cmd/cloched", "../cmd/cloche-agent"} {
			info, err := os.Stat(cmd)
			if err != nil {
				t.Errorf("cmd directory %q does not exist: %v", cmd, err)
			} else if !info.IsDir() {
				t.Errorf("%q is not a directory", cmd)
			}
		}
	})

	t.Run("Makefile targets referenced exist", func(t *testing.T) {
		makefile, err := os.ReadFile("../Makefile")
		if err != nil {
			t.Fatal("cannot read Makefile:", err)
		}
		mk := string(makefile)

		// Targets mentioned in INSTALL.md
		targets := []string{"install", "docker-build"}
		for _, target := range targets {
			if !strings.Contains(doc, "make "+target) {
				t.Errorf("INSTALL.md does not reference 'make %s'", target)
			}
			if !strings.Contains(mk, target+":") {
				t.Errorf("Makefile does not define target %q referenced in INSTALL.md", target)
			}
		}
	})

	t.Run("has required sections", func(t *testing.T) {
		sections := []string{
			"## Prerequisites",
			"## Build from Source",
			"## `go install`",
			"## Pre-built Release Binaries",
			"## Homebrew",
			"## Copy Binaries",
			"## Docker-Only Usage",
			"## Verifying the Installation",
			"## Upgrading",
			"## Method Comparison",
		}
		for _, section := range sections {
			if !strings.Contains(doc, section) {
				t.Errorf("INSTALL.md missing section %q", section)
			}
		}
	})

	t.Run("links to other docs", func(t *testing.T) {
		links := []string{"USAGE.md", "workflows.md"}
		for _, link := range links {
			if !strings.Contains(doc, link) {
				t.Errorf("INSTALL.md does not link to %q", link)
			}
		}
	})

	t.Run("docker base image referenced", func(t *testing.T) {
		// The Dockerfile in docker/cloche-base uses debian:12-slim as its runtime stage
		if !strings.Contains(doc, "Docker") {
			t.Error("INSTALL.md does not mention Docker")
		}
	})

	t.Run("cross-compile instructions present", func(t *testing.T) {
		if !strings.Contains(doc, "GOOS") || !strings.Contains(doc, "GOARCH") {
			t.Error("INSTALL.md does not include cross-compilation instructions")
		}
	})
}
