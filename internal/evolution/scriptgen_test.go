package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptGenerator_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	resp, _ := json.Marshal(scriptResponse{
		Path:    "../../../etc/cron.d/evil",
		Content: "#!/bin/bash\necho pwned",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	_, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain '..'")
}

func TestScriptGenerator_PathOutsideScripts(t *testing.T) {
	dir := t.TempDir()
	resp, _ := json.Marshal(scriptResponse{
		Path:    "src/evil.sh",
		Content: "#!/bin/bash\necho pwned",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	_, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with scripts/ or .cloche/scripts/")
}

func TestScriptGenerator_NoShebang(t *testing.T) {
	dir := t.TempDir()
	resp, _ := json.Marshal(scriptResponse{
		Path:    "scripts/check.sh",
		Content: "echo no shebang",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	_, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shebang")
}

func TestScriptGenerator_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "scripts", "check.sh")
	os.MkdirAll(filepath.Dir(scriptPath), 0755)
	os.WriteFile(scriptPath, []byte("#!/bin/bash\nold"), 0644)

	resp, _ := json.Marshal(scriptResponse{
		Path:    "scripts/check.sh",
		Content: "#!/bin/bash\nnew",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	_, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Verify old content is preserved
	data, _ := os.ReadFile(scriptPath)
	assert.Equal(t, "#!/bin/bash\nold", string(data))
}

func TestScriptGenerator_ValidScript(t *testing.T) {
	dir := t.TempDir()
	resp, _ := json.Marshal(scriptResponse{
		Path:    "scripts/lint.sh",
		Content: "#!/bin/bash\nset -e\ngo vet ./...",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	result, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.NoError(t, err)
	assert.Equal(t, "scripts/lint.sh", result.Path)

	// Verify file was written non-executable
	info, err := os.Stat(filepath.Join(dir, "scripts", "lint.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestScriptGenerator_ClocheScriptsPath(t *testing.T) {
	dir := t.TempDir()
	resp, _ := json.Marshal(scriptResponse{
		Path:    ".cloche/scripts/check.sh",
		Content: "#!/bin/sh\nexit 0",
	})
	gen := &ScriptGenerator{LLM: &fakeLLM{response: string(resp)}}

	result, err := gen.Generate(context.Background(), dir, &Lesson{Insight: "test", SuggestedAction: "test"})
	require.NoError(t, err)
	assert.Equal(t, ".cloche/scripts/check.sh", result.Path)
}

func TestValidateScriptPath(t *testing.T) {
	tests := []struct {
		path    string
		wantErr bool
	}{
		{"scripts/check.sh", false},
		{".cloche/scripts/check.sh", false},
		{"scripts/sub/check.sh", false},
		{"../scripts/check.sh", true},
		{"scripts/../../../etc/passwd", true},
		{"src/main.go", true},
		{"prompts/foo.md", true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			err := validateScriptPath(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
