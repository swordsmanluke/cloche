package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptTaskAssigner_ParsesJSONArray(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a script that outputs a JSON array
	script := filepath.Join(tmpDir, "list-tasks.sh")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
echo '[{"id":"task-1","title":"Fix bug","description":"Fix the login bug"},{"id":"task-2","title":"Add feature","description":"Add dark mode"}]'
`), 0755))

	assigner := &ScriptTaskAssigner{Command: "bash " + script}
	tasks, err := assigner.ListTasks(context.Background(), tmpDir)
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Equal(t, "Fix bug", tasks[0].Title)
	assert.Equal(t, "Fix the login bug", tasks[0].Description)

	assert.Equal(t, "task-2", tasks[1].ID)
	assert.Equal(t, "Add feature", tasks[1].Title)
}

func TestScriptTaskAssigner_EmptyArray(t *testing.T) {
	tmpDir := t.TempDir()

	script := filepath.Join(tmpDir, "list-tasks.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho '[]'\n"), 0755))

	assigner := &ScriptTaskAssigner{Command: "bash " + script}
	tasks, err := assigner.ListTasks(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestScriptTaskAssigner_EmptyOutput(t *testing.T) {
	tmpDir := t.TempDir()

	script := filepath.Join(tmpDir, "list-tasks.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho ''\n"), 0755))

	assigner := &ScriptTaskAssigner{Command: "bash " + script}
	tasks, err := assigner.ListTasks(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestScriptTaskAssigner_CommandFailure(t *testing.T) {
	tmpDir := t.TempDir()

	assigner := &ScriptTaskAssigner{Command: "exit 1"}
	_, err := assigner.ListTasks(context.Background(), tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running list-tasks command")
}

func TestScriptTaskAssigner_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	script := filepath.Join(tmpDir, "list-tasks.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'not json'\n"), 0755))

	assigner := &ScriptTaskAssigner{Command: "bash " + script}
	_, err := assigner.ListTasks(context.Background(), tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing list-tasks output")
}

func TestScriptTaskAssigner_PartialFields(t *testing.T) {
	tmpDir := t.TempDir()

	// Tasks with only id field
	script := filepath.Join(tmpDir, "list-tasks.sh")
	require.NoError(t, os.WriteFile(script, []byte(`#!/bin/sh
echo '[{"id":"task-1"}]'
`), 0755))

	assigner := &ScriptTaskAssigner{Command: "bash " + script}
	tasks, err := assigner.ListTasks(context.Background(), tmpDir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Empty(t, tasks[0].Title)
}

// --- JSONL parsing tests ---

func TestParseTasksJSONL_Basic(t *testing.T) {
	data := `{"id":"task-1","status":"open","title":"Fix bug","description":"Fix login bug"}
{"id":"task-2","status":"closed","title":"Add feature","description":"Dark mode"}`

	tasks, err := ParseTasksJSONL(data)
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Equal(t, "open", tasks[0].Status)
	assert.Equal(t, "Fix bug", tasks[0].Title)
	assert.Equal(t, "Fix login bug", tasks[0].Description)

	assert.Equal(t, "task-2", tasks[1].ID)
	assert.Equal(t, "closed", tasks[1].Status)
}

func TestParseTasksJSONL_WithMetadata(t *testing.T) {
	data := `{"id":"task-1","status":"open","title":"Fix bug","metadata":{"priority":"high","assignee":"alice"}}`

	tasks, err := ParseTasksJSONL(data)
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Equal(t, "high", tasks[0].Metadata["priority"])
	assert.Equal(t, "alice", tasks[0].Metadata["assignee"])
}

func TestParseTasksJSONL_EmptyLines(t *testing.T) {
	data := `
{"id":"task-1","status":"open"}

{"id":"task-2","status":"in-progress"}
`

	tasks, err := ParseTasksJSONL(data)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	assert.Equal(t, "task-1", tasks[0].ID)
	assert.Equal(t, "task-2", tasks[1].ID)
}

func TestParseTasksJSONL_Empty(t *testing.T) {
	tasks, err := ParseTasksJSONL("")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestParseTasksJSONL_InvalidJSON(t *testing.T) {
	data := `{"id":"task-1"}
not json
{"id":"task-2"}`

	_, err := ParseTasksJSONL(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing JSONL line 2")
}

func TestParseTasksJSONL_AllStatuses(t *testing.T) {
	data := `{"id":"t1","status":"open"}
{"id":"t2","status":"closed"}
{"id":"t3","status":"in-progress"}`

	tasks, err := ParseTasksJSONL(data)
	require.NoError(t, err)
	require.Len(t, tasks, 3)

	assert.Equal(t, "open", tasks[0].Status)
	assert.Equal(t, "closed", tasks[1].Status)
	assert.Equal(t, "in-progress", tasks[2].Status)
}

// --- Task.IsOpen tests ---

func TestTask_IsOpen(t *testing.T) {
	tests := []struct {
		status string
		open   bool
	}{
		{"open", true},
		{"", true},  // backward compatible: empty status is open
		{"closed", false},
		{"in-progress", false},
	}

	for _, tt := range tests {
		task := Task{ID: "t1", Status: tt.status}
		assert.Equal(t, tt.open, task.IsOpen(), "status=%q", tt.status)
	}
}
