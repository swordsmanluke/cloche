package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// trackerRequest is a parsed `cloche tasks create|update|find` invocation:
// the tracker phase to run and its contract inputs.
type trackerRequest struct {
	Path       string // "create" or "update" (the daemon endpoint under /tasks/)
	ProjectDir string
	Inputs     map[string]string
}

// isTrackerSubcommand reports whether args (after "tasks") select one of the
// tracker phase subcommands rather than the plain task listing.
func isTrackerSubcommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "create", "update", "find":
		return true
	}
	return false
}

// parseTrackerArgs parses the arguments after "tasks" for create, update and
// find. readFile loads --body-file.
func parseTrackerArgs(args []string, readFile func(string) ([]byte, error)) (*trackerRequest, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing subcommand")
	}
	sub := args[0]
	rest := args[1:]
	req := &trackerRequest{Inputs: map[string]string{}}
	var labels, addLabels, removeLabels []string

	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		next := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(rest) {
				return "", fmt.Errorf("flag %s needs a value", name)
			}
			i++
			return rest[i], nil
		}
		v, err := next()
		if err != nil {
			return nil, err
		}
		switch name {
		case "--project":
			req.ProjectDir = v
		case "--title":
			req.Inputs["title"] = v
		case "--body":
			req.Inputs["body"] = v
		case "--body-file":
			data, err := readFile(v)
			if err != nil {
				return nil, fmt.Errorf("reading --body-file: %w", err)
			}
			req.Inputs["body"] = string(data)
		case "--priority":
			req.Inputs["priority"] = v
		case "--label":
			labels = append(labels, v)
		case "--add-label":
			addLabels = append(addLabels, v)
		case "--remove-label":
			removeLabels = append(removeLabels, v)
		case "--source":
			req.Inputs["signal_source"] = v
		case "--key":
			req.Inputs["signal_key"] = v
		case "--comment":
			req.Inputs["comment"] = v
		default:
			return nil, fmt.Errorf("unknown flag %s", name)
		}
	}

	allowed := map[string]map[string]bool{
		"create": {"title": true, "body": true, "priority": true, "labels": true, "signal_source": true, "signal_key": true},
		"update": {"comment": true, "priority": true, "add_labels": true, "remove_labels": true},
		"find":   {"signal_source": true, "signal_key": true},
	}
	if len(labels) > 0 {
		req.Inputs["labels"] = strings.Join(labels, ",")
	}
	if len(addLabels) > 0 {
		req.Inputs["add_labels"] = strings.Join(addLabels, ",")
	}
	if len(removeLabels) > 0 {
		req.Inputs["remove_labels"] = strings.Join(removeLabels, ",")
	}
	if _, ok := allowed[sub]; !ok {
		return nil, fmt.Errorf("unknown subcommand %q", sub)
	}
	for k := range req.Inputs {
		if !allowed[sub][k] {
			return nil, fmt.Errorf("%s is not valid for `tasks %s`", k, sub)
		}
	}

	switch sub {
	case "create":
		req.Path = "create"
		if len(positional) > 0 {
			return nil, fmt.Errorf("unexpected argument %q", positional[0])
		}
		if req.Inputs["title"] == "" {
			return nil, fmt.Errorf("--title is required")
		}
		if req.Inputs["signal_key"] != "" && req.Inputs["signal_source"] == "" {
			return nil, fmt.Errorf("--key requires --source")
		}
	case "update":
		req.Path = "update"
		if len(positional) != 1 {
			return nil, fmt.Errorf("usage: cloche tasks update <task-id> [--comment ...] [--priority N] [--add-label L] [--remove-label L]")
		}
		req.Inputs["action"] = "update"
		req.Inputs["target_task_id"] = positional[0]
		if len(req.Inputs) == 2 {
			return nil, fmt.Errorf("nothing to update: set --comment, --priority, --add-label or --remove-label")
		}
	case "find":
		req.Path = "update"
		if len(positional) > 0 {
			return nil, fmt.Errorf("unexpected argument %q", positional[0])
		}
		if req.Inputs["signal_source"] == "" || req.Inputs["signal_key"] == "" {
			return nil, fmt.Errorf("--source and --key are required")
		}
		req.Inputs["action"] = "find"
	}
	return req, nil
}

// cmdTasksTracker runs `cloche tasks create|update|find` through the daemon's
// HTTP API, which runs the project's bound create-task / update-task phase.
func cmdTasksTracker(args []string) {
	req, err := parseTrackerArgs(args, os.ReadFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	httpAddr := resolveHTTPAddr()
	if httpAddr == "" {
		httpAddr = "localhost:8080"
	}
	if req.ProjectDir == "" {
		req.ProjectDir, _ = os.Getwd()
	}
	out, err := postTrackerPhase(httpAddr, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	switch args[0] {
	case "create":
		fmt.Println(out["new_task_id"])
	case "update":
		fmt.Println("ok")
	case "find":
		if out["task_found"] != "true" || out["found_task_id"] == "" {
			fmt.Fprintln(os.Stderr, "no open task for that source and key")
			os.Exit(1)
		}
		fmt.Println(out["found_task_id"])
	}
}

// postTrackerPhase POSTs req to the daemon and returns the phase's outputs.
func postTrackerPhase(httpAddr string, req *trackerRequest) (map[string]string, error) {
	body, _ := json.Marshal(map[string]any{"inputs": req.Inputs})
	endpoint := fmt.Sprintf("http://%s/api/projects/%s/tasks/%s", httpAddr, url.PathEscape(req.ProjectDir), req.Path)
	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon web API: %w\nhint: set CLOCHE_HTTP (or [daemon] http in ~/.config/cloche/config) and restart the daemon", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var r struct {
		Outputs map[string]string `json:"outputs"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return r.Outputs, nil
}
