package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const historyFile = ".cloche/history.log"

// AppendHistory appends a step completion entry to the history log.
// For agent steps, pass nil for output (only the header is recorded).
// For script steps, the full cleaned output is included, indented with "  | ".
func AppendHistory(workDir, stepName, result string, isAgent bool, output []byte) {
	path := filepath.Join(workDir, historyFile)
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	ts := time.Now().UTC().Format(time.RFC3339)
	var entry string
	if isAgent {
		entry = fmt.Sprintf("[%s] step:%s result:%s (agent)\n\n", ts, stepName, result)
	} else {
		entry = fmt.Sprintf("[%s] step:%s result:%s\n", ts, stepName, result)
		if len(output) > 0 {
			trimmed := strings.TrimRight(string(output), "\n")
			if trimmed != "" {
				for _, line := range strings.Split(trimmed, "\n") {
					entry += "  | " + line + "\n"
				}
			}
		}
		entry += "\n"
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(entry)
}

// AppendHistoryMarker appends a workflow-level marker (start/end) to the history log.
func AppendHistoryMarker(workDir, marker string) {
	path := filepath.Join(workDir, historyFile)
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	ts := time.Now().UTC().Format(time.RFC3339)
	entry := fmt.Sprintf("[%s] %s\n\n", ts, marker)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(entry)
}
