package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cloche-dev/cloche/internal/adapters/beads"
)

func main() {
	dir := "/home/lucas/workspace/cloche"
	t := beads.NewTracker(dir)
	tasks, err := t.ListReady(context.Background(), dir)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf("Ready tasks: %d\n", len(tasks))
	for _, task := range tasks {
		fmt.Printf("  %s: %s\n", task.ID, task.Title)
	}

	// Also raw check
	data, _ := os.ReadFile(dir + "/.beads/issues.jsonl")
	seen := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		id, _ := m["id"].(string)
		status, _ := m["status"].(string)
		if status == "tombstone" {
			delete(seen, id)
		} else {
			seen[id] = status
		}
	}
	openCount := 0
	for id, st := range seen {
		if st == "open" {
			fmt.Printf("  raw open: %s\n", id)
			openCount++
		}
	}
	fmt.Printf("Raw open count: %d\n", openCount)
}
