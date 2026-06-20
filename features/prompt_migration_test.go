package features_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cucumber/godog"
)

// promptMigrationCtx holds per-scenario state for prompt-migration BDD scenarios.
type promptMigrationCtx struct {
	fileContent    string            // content of the single file under test
	multipleFiles  map[string]string // filename → content for multi-file scenarios
}

func (s *promptMigrationCtx) reset() {
	*s = promptMigrationCtx{}
}

// projectRoot returns the root of the cloche module by walking up from this source file.
func promptMigrationProjectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..")
}

// ─── Given steps ─────────────────────────────────────────────────────────────

func (s *promptMigrationCtx) theClochePrmoptFile(name string) error {
	path := filepath.Join(promptMigrationProjectRoot(), ".cloche", "prompts", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading prompt file %q: %w", path, err)
	}
	s.fileContent = string(data)
	return nil
}

func (s *promptMigrationCtx) theContainerSideVerticalPromptFiles() error {
	names := []string{
		"vertical-implement.md",
		"vertical-self-review.md",
		"vertical-bdd-test-plan.md",
		"vertical-update-docs.md",
		"vertical-address-feedback.md",
	}
	s.multipleFiles = make(map[string]string, len(names))
	dir := filepath.Join(promptMigrationProjectRoot(), ".cloche", "prompts")
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("reading %q: %w", name, err)
		}
		s.multipleFiles[name] = string(data)
	}
	return nil
}

func (s *promptMigrationCtx) theClocheScriptFile(name string) error {
	path := filepath.Join(promptMigrationProjectRoot(), ".cloche", "scripts", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading script file %q: %w", path, err)
	}
	s.fileContent = string(data)
	return nil
}

// ─── Then steps: single-file assertions ──────────────────────────────────────

func (s *promptMigrationCtx) thePromptFileDoesNotContain(text string) error {
	if strings.Contains(s.fileContent, text) {
		return fmt.Errorf("prompt file unexpectedly contains %q", text)
	}
	return nil
}

func (s *promptMigrationCtx) thePromptFileContains(text string) error {
	if !strings.Contains(s.fileContent, text) {
		return fmt.Errorf("prompt file does not contain %q", text)
	}
	return nil
}

func (s *promptMigrationCtx) thePromptFileMentions(text string) error {
	return s.thePromptFileContains(text)
}

// thePromptFileOpensWithAHeaderBlock checks that the file begins with one or more
// lines of the form "KEY = value" (the header-block format), followed by a blank line.
func (s *promptMigrationCtx) thePromptFileOpensWithAHeaderBlock() error {
	return assertHeaderBlock(s.fileContent)
}

// theHeaderBlockContains checks that the header section (before the first blank line)
// contains the given key name.
func (s *promptMigrationCtx) theHeaderBlockContains(key string) error {
	header := headerSection(s.fileContent)
	if !strings.Contains(header, key) {
		return fmt.Errorf("header block does not contain %q\nheader:\n%s", key, header)
	}
	return nil
}

func (s *promptMigrationCtx) theScriptFileContains(text string) error {
	if !strings.Contains(s.fileContent, text) {
		return fmt.Errorf("script file does not contain %q", text)
	}
	return nil
}

// ─── Then steps: multi-file assertions ───────────────────────────────────────

func (s *promptMigrationCtx) noneOfThePromptFilesContain(text string) error {
	for name, content := range s.multipleFiles {
		if strings.Contains(content, text) {
			return fmt.Errorf("prompt file %q contains %q", name, text)
		}
	}
	return nil
}

func (s *promptMigrationCtx) allPromptFilesOpenWithAHeaderBlock() error {
	for name, content := range s.multipleFiles {
		if err := assertHeaderBlock(content); err != nil {
			return fmt.Errorf("prompt file %q: %w", name, err)
		}
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// headerSection returns the lines before the first blank line (the header block).
func headerSection(content string) string {
	lines := strings.Split(content, "\n")
	var header []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			break
		}
		header = append(header, line)
	}
	return strings.Join(header, "\n")
}

// assertHeaderBlock returns an error if content does not open with a header block.
// A valid header block has at least one line matching "KEY = ..." before the first
// blank line, and is followed by a blank line.
func assertHeaderBlock(content string) error {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return fmt.Errorf("file is empty")
	}
	headerLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			break
		}
		headerLines++
	}
	if headerLines == 0 {
		return fmt.Errorf("file begins with a blank line; expected header block")
	}
	// At least one header line must look like "KEY = ..." (uppercase key, space-equals-space).
	found := false
	for _, line := range lines[:headerLines] {
		parts := strings.SplitN(line, " = ", 2)
		if len(parts) == 2 && parts[0] == strings.ToUpper(parts[0]) && parts[0] != "" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no header line of the form 'KEY = value' found before the first blank line\nfirst %d line(s):\n%s",
			headerLines, strings.Join(lines[:headerLines], "\n"))
	}
	// After the header lines there must be a blank line separator.
	if headerLines >= len(lines) || strings.TrimSpace(lines[headerLines]) != "" {
		return fmt.Errorf("header block is not followed by a blank line")
	}
	return nil
}

// ─── Step registration ────────────────────────────────────────────────────────

func initPromptMigrationScenarios(ctx *godog.ScenarioContext) {
	s := &promptMigrationCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Given
	ctx.Step(`^the cloche prompt file "([^"]*)"$`, s.theClochePrmoptFile)
	ctx.Step(`^the container-side vertical prompt files$`, s.theContainerSideVerticalPromptFiles)
	ctx.Step(`^the cloche script file "([^"]*)"$`, s.theClocheScriptFile)

	// Then — single file
	ctx.Step(`^the prompt file does not contain "([^"]*)"$`, s.thePromptFileDoesNotContain)
	ctx.Step(`^the prompt file contains "([^"]*)"$`, s.thePromptFileContains)
	ctx.Step(`^the prompt file mentions "([^"]*)"$`, s.thePromptFileMentions)
	ctx.Step(`^the prompt file opens with a header block$`, s.thePromptFileOpensWithAHeaderBlock)
	ctx.Step(`^the header block contains "([^"]*)"$`, s.theHeaderBlockContains)
	ctx.Step(`^the script file contains "([^"]*)"$`, s.theScriptFileContains)

	// Then — multiple files
	ctx.Step(`^none of the prompt files contain "([^"]*)"$`, s.noneOfThePromptFilesContain)
	ctx.Step(`^all prompt files open with a header block$`, s.allPromptFilesOpenWithAHeaderBlock)
}

// suppress unused import warning during compilation
var _ = strings.Contains
