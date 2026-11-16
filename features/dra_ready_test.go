package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cucumber/godog"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/draauth"
)

func init() { registerScenarios(initDRAReadyScenarios) }

const draReadyProject = "/home/user/projects/ready-app"

// draReadyTracker is the in-memory task tracker behind the create-task and
// update-task phases (the tracker module is outside this feature). It
// implements web.TaskProvider and web.TrackerPhaseRunner.
type draReadyTracker struct {
	labels  map[string][]string
	created int
	n       int
}

func (t *draReadyTracker) GetLoopTasks(string) []web.TaskEntry { return nil }
func (t *draReadyTracker) ReleaseTask(context.Context, string, string) error {
	return nil
}
func (t *draReadyTracker) CloseTask(context.Context, string, string) error { return nil }
func (t *draReadyTracker) RunOnce(context.Context, string, string, string, string) (string, error) {
	return "", nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (t *draReadyTracker) RunTrackerPhase(_ context.Context, _, slot string, in map[string]string) (map[string]string, error) {
	switch slot {
	case "create-task":
		t.n++
		t.created++
		id := fmt.Sprintf("new-%d", t.n)
		t.labels[id] = splitCSV(in["labels"])
		return map[string]string{"new_task_id": id}, nil
	case "update-task":
		id := in["target_task_id"]
		switch in["action"] {
		case "get":
			l, ok := t.labels[id]
			if !ok {
				return map[string]string{"task_found": "false"}, nil
			}
			return map[string]string{"task_found": "true", "task_labels": strings.Join(l, ",")}, nil
		case "update":
			t.labels[id] = append(t.labels[id], splitCSV(in["add_labels"])...)
			return map[string]string{"task_found": "true"}, nil
		}
	}
	return nil, fmt.Errorf("unexpected phase %s", slot)
}

func (t *draReadyTracker) has(id, label string) bool {
	for _, l := range t.labels[id] {
		if l == label {
			return true
		}
	}
	return false
}

type draReadyWorld struct {
	domain  string
	h       *web.Handler
	tracker *draReadyTracker
	token   string
	taskID  string // the task the scenario acts on
	code    int
	body    string
	docs    string
}

type draReadyKey struct{}

// legacyReadyRefusal is the message today's API gives for any ready label.
const legacyReadyRefusal = "a DRA cannot mark a task 'ready'"

// pendingL2 marks an outcome that depends on the API change (L2): while the
// API still refuses ready outright, the step is pending rather than failing.
func (w *draReadyWorld) pendingL2() error {
	if strings.Contains(w.body, legacyReadyRefusal) {
		return godog.ErrPending // pending: L2
	}
	return nil
}

func (w *draReadyWorld) post(path string, inputs map[string]string) {
	// Marshalling a map of strings cannot fail.
	b, _ := json.Marshal(map[string]any{"inputs": inputs})
	req := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
	req.RemoteAddr = "172.17.0.2:4000" // a container, not loopback
	req.Header.Set("Authorization", "Bearer "+w.token)
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, req)
	w.code, w.body = rec.Code, rec.Body.String()
}

func (w *draReadyWorld) create(labels string) error {
	w.post("/api/dra/tasks/create", map[string]string{"title": "a task", "labels": labels})
	if w.code == http.StatusOK {
		var r struct {
			Outputs map[string]string `json:"outputs"`
		}
		if err := json.Unmarshal([]byte(w.body), &r); err != nil {
			return fmt.Errorf("decoding create response %q: %w", w.body, err)
		}
		w.taskID = r.Outputs["new_task_id"]
	}
	return nil
}

func (w *draReadyWorld) update(add string) {
	w.post("/api/dra/tasks/update", map[string]string{"action": "update", "target_task_id": w.taskID, "add_labels": add})
}

func (w *draReadyWorld) seed(labels ...string) {
	w.taskID = "seed-1"
	w.tracker.labels[w.taskID] = labels
}

func initDRAReadyScenarios(ctx *godog.ScenarioContext) {
	ctx.Before(func(cx context.Context, _ *godog.Scenario) (context.Context, error) {
		return context.WithValue(cx, draReadyKey{}, &draReadyWorld{}), nil
	})
	W := func(cx context.Context) *draReadyWorld { return cx.Value(draReadyKey{}).(*draReadyWorld) }

	// Setup steps.
	ctx.Step(`^a DRA for the domain "([^"]*)"$`, func(cx context.Context, domainName string) error {
		w := W(cx)
		store, err := sqlite.NewStore(":memory:")
		if err != nil {
			return err
		}
		secret := []byte("dra-ready-secret")
		run := domain.NewRun("wake-1", "dra-wake-"+domainName)
		run.ProjectDir = draReadyProject
		run.State = domain.RunStateRunning
		if err := store.CreateRun(cx, run); err != nil {
			return err
		}
		w.tracker = &draReadyTracker{labels: map[string][]string{}}
		w.h, err = web.NewHandler(store, store, web.WithDRAAccess(secret), web.WithTaskProvider(w.tracker))
		if err != nil {
			return err
		}
		w.domain = domainName
		w.token = draauth.Token(secret, draauth.Claims{RunID: "wake-1", Project: draReadyProject, DRA: domainName})
		return nil
	})
	ctx.Step(`^a task labelled "([^"]*)" that is not ready$`, func(cx context.Context, label string) error {
		W(cx).seed(label)
		return nil
	})
	ctx.Step(`^a task with no domain label that is not ready$`, func(cx context.Context) error {
		W(cx).seed()
		return nil
	})
	// The cap itself is existing loop behaviour; this feature changes nothing
	// there. Recorded here, asserted in the L2 outcome step below.
	ctx.Step(`^the daily budget cap is exceeded$`, func() error { return nil })

	// Actions.
	ctx.Step(`^the DRA creates a task labelled "([^"]*)" and "([^"]*)"$`, func(cx context.Context, a, b string) error {
		return W(cx).create(a + "," + b)
	})
	ctx.Step(`^the DRA creates a task labelled "([^"]*)"$`, func(cx context.Context, a string) error {
		return W(cx).create(a)
	})
	ctx.Step(`^the DRA creates a task with no domain label and the "([^"]*)" label$`, func(cx context.Context, a string) error {
		return W(cx).create(a)
	})
	ctx.Step(`^the DRA updates the task to add the "([^"]*)" label$`, func(cx context.Context, a string) error {
		W(cx).update(a)
		return nil
	})
	ctx.Step(`^the DRA updates the task to add the "([^"]*)" and "([^"]*)" labels$`, func(cx context.Context, a, b string) error {
		W(cx).update(a + "," + b)
		return nil
	})

	// Outcomes. Success outcomes are pending: L2 while the API still refuses
	// ready; once L2 lands they assert for real.
	succeeded := func(w *draReadyWorld, what string) error {
		if err := w.pendingL2(); err != nil {
			return err
		}
		if w.code != http.StatusOK {
			return fmt.Errorf("%s: want 200, got %d: %s", what, w.code, w.body)
		}
		return nil
	}
	ctx.Step(`^the task is created$`, func(cx context.Context) error { return succeeded(W(cx), "create") })
	ctx.Step(`^the update is accepted$`, func(cx context.Context) error { return succeeded(W(cx), "update") })
	ctx.Step(`^the request is refused$`, func(cx context.Context) error {
		if w := W(cx); w.code != http.StatusForbidden {
			return fmt.Errorf("want 403, got %d: %s", w.code, w.body)
		}
		return nil
	})
	ctx.Step(`^the error names the domain "([^"]*)"$`, func(cx context.Context, d string) error {
		w := W(cx)
		if err := w.pendingL2(); err != nil {
			return err
		}
		if !strings.Contains(w.body, d) {
			return fmt.Errorf("error does not name domain %q: %s", d, w.body)
		}
		return nil
	})
	ctx.Step(`^no task is created$`, func(cx context.Context) error {
		if n := W(cx).tracker.created; n != 0 {
			return fmt.Errorf("%d task(s) created", n)
		}
		return nil
	})
	ctx.Step(`^the task carries the "([^"]*)" label$`, func(cx context.Context, l string) error {
		w := W(cx)
		if err := w.pendingL2(); err != nil {
			return err
		}
		if !w.tracker.has(w.taskID, l) {
			return fmt.Errorf("task %q labels %v lack %q", w.taskID, w.tracker.labels[w.taskID], l)
		}
		return nil
	})
	ctx.Step(`^the task does not carry the "([^"]*)" label$`, func(cx context.Context, l string) error {
		w := W(cx)
		if w.tracker.has(w.taskID, l) {
			return fmt.Errorf("task %q unexpectedly carries %q", w.taskID, l)
		}
		return nil
	})
	ctx.Step(`^the loop does not start a run for the task$`, func() error {
		return godog.ErrPending // pending: L2
	})

	// Docs scenario.
	ctx.Step(`^the DRA documentation is read$`, func(cx context.Context) error {
		b, err := os.ReadFile(filepath.Join("..", "docs", "dras.md"))
		W(cx).docs = string(b)
		return err
	})
	ctx.Step(`^it states that a DRA may mark any task in its own domain ready on create and update$`, func(cx context.Context) error {
		d := strings.Join(strings.Fields(W(cx).docs), " ")
		if !strings.Contains(d, "may mark any task in its own domain `ready`, on create and on update") {
			return fmt.Errorf("docs/dras.md does not state the ruling")
		}
		return nil
	})
	ctx.Step(`^it names ruling "([^"]*)"$`, func(cx context.Context, r string) error {
		if !strings.Contains(W(cx).docs, r) {
			return fmt.Errorf("docs/dras.md does not name %s", r)
		}
		return nil
	})
	ctx.Step(`^it no longer says a DRA cannot set ready$`, func(cx context.Context) error {
		d := strings.Join(strings.Fields(W(cx).docs), " ")
		if m := regexp.MustCompile(`(?i)DRA\s+\*{0,2}cannot\*{0,2}\s+(set|mark)[^.]*ready`).FindString(d); m != "" {
			return fmt.Errorf("docs/dras.md still says: %q", m)
		}
		if strings.Contains(d, "Open question") {
			return fmt.Errorf("docs/dras.md still carries the open question")
		}
		return nil
	})
}
