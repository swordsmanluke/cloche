package main

import (
	"bytes"
	"errors"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DRA mode: inside a DRA wake-up container the daemon sets CLOCHE_DRA_URL and
// CLOCHE_DRA_TOKEN. The CLI then reaches the daemon only through its DRA API,
// authenticated by that per-wake-up credential, and only for what a DRA may
// do: list tasks and runs, read run status and output, create a task, update
// or close a task of its domain, ask, await and read questions, and register,
// list and cancel its own waits. Every other command is refused here
// as a courtesy; the daemon enforces the same limits itself.

type draClient struct {
	base, token string
}

// draModeClient returns the client for the environment's credential, or nil
// outside a DRA container.
func draModeClient() *draClient {
	base := strings.TrimRight(os.Getenv("CLOCHE_DRA_URL"), "/")
	tok := os.Getenv("CLOCHE_DRA_TOKEN")
	if base == "" || tok == "" {
		return nil
	}
	return &draClient{base: base, token: tok}
}

// do sends a request and returns the body, turning a non-200 into an error
// carrying the daemon's message.
func (c *draClient) do(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr) //nolint:noctx
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := &http.Client{Timeout: 60 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
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
	return data, nil
}

func (c *draClient) phase(path string, inputs map[string]string) (map[string]string, error) {
	data, err := c.do("POST", "/api/dra/tasks/"+path, map[string]any{"inputs": inputs})
	if err != nil {
		return nil, err
	}
	var r struct {
		Outputs map[string]string `json:"outputs"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return r.Outputs, nil
}

// runDRACommand handles one CLI invocation in DRA mode. It returns false for
// the few commands that behave the same everywhere (help, version, get, set)
// so main handles them as usual.
func runDRACommand(c *draClient, args []string) (handled bool, err error) {
	switch args[0] {
	case "help", "--help", "-h", "version", "-v", "--version", "get", "set":
		return false, nil
	case "tasks":
		return true, c.tasks(args[1:])
	case "list":
		return true, c.listRuns()
	case "status":
		if len(args) != 2 {
			return true, fmt.Errorf("usage: cloche status <run-id>")
		}
		data, err := c.do("GET", "/api/dra/runs/"+url.PathEscape(args[1]), nil)
		if err == nil {
			var buf bytes.Buffer
			if json.Indent(&buf, data, "", "  ") == nil {
				data = buf.Bytes()
			}
			fmt.Println(string(data))
		}
		return true, err
	case "logs":
		return true, c.logs(args[1:])
	case "dra":
		if len(args) > 1 && (args[1] == "list" || args[1] == "wake" || args[1] == "heartbeat") {
			return true, fmt.Errorf("`cloche dra %s` is not available to a DRA (allowed: dra wait, dra waits)", args[1])
		}
		return true, draCommand(args[1:], os.Stdout) // wait, waits; anything else prints the usage
	case "questions":
		if len(args) > 1 && (args[1] == "ask" || args[1] == "await" || args[1] == "read") {
			err := questionsCommand(args[1:], os.Stdout)
			var ee questionsExitError
			if errors.As(err, &ee) {
				os.Exit(ee.code) // "no answer yet": an exit code, not an error line
			}
			return true, err
		}
		return true, fmt.Errorf("`cloche questions` is limited to ask, await and read for a DRA")
	}
	return true, fmt.Errorf("`cloche %s` is not available to a DRA (allowed: tasks, list, status, logs, questions, dra wait)", args[0])
}

func (c *draClient) tasks(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		data, err := c.do("GET", "/api/dra/tasks", nil)
		if err != nil {
			return err
		}
		var tasks []struct {
			ID, Status, Title string
		}
		if err := json.Unmarshal(data, &tasks); err != nil {
			return err
		}
		for _, t := range tasks {
			fmt.Printf("%-20s  %-12s  %s\n", t.ID, t.Status, t.Title)
		}
		return nil
	}
	if args[0] == "close" {
		if len(args) != 2 {
			return fmt.Errorf("usage: cloche tasks close <task-id>")
		}
		if _, err := c.do("POST", "/api/dra/tasks/close", map[string]string{"task_id": args[1]}); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	if !isTrackerSubcommand(args) {
		return fmt.Errorf("unknown tasks subcommand %q (allowed: create, update, find, close)", args[0])
	}
	req, err := parseTrackerArgs(args, os.ReadFile)
	if err != nil {
		return err
	}
	out, err := c.phase(req.Path, req.Inputs)
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		fmt.Println(out["new_task_id"])
	case "update":
		fmt.Println("ok")
	case "find":
		if out["task_found"] != "true" || out["found_task_id"] == "" {
			return fmt.Errorf("no open task of this domain for that source and key")
		}
		fmt.Println(out["found_task_id"])
	}
	return nil
}

func (c *draClient) listRuns() error {
	data, err := c.do("GET", "/api/dra/runs", nil)
	if err != nil {
		return err
	}
	var runs []struct {
		ID           string `json:"id"`
		WorkflowName string `json:"workflow_name"`
		State        string `json:"state"`
		Title        string `json:"title"`
	}
	if err := json.Unmarshal(data, &runs); err != nil {
		return err
	}
	for _, r := range runs {
		fmt.Printf("%-32s  %-16s  %-10s  %s\n", r.ID, r.WorkflowName, r.State, r.Title)
	}
	return nil
}

func (c *draClient) logs(args []string) error {
	var runID, step string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--step" || a == "-s":
			if i+1 >= len(args) {
				return fmt.Errorf("--step needs a value")
			}
			i++
			step = args[i]
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("flag %s is not available to a DRA", a)
		case runID == "":
			runID = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if runID == "" || step == "" {
		return fmt.Errorf("usage: cloche logs <run-id> --step <step>")
	}
	data, err := c.do("GET", "/api/dra/runs/"+url.PathEscape(runID)+"/steps/"+url.PathEscape(step)+"/output", nil)
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	return nil
}
