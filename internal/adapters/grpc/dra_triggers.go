package grpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// DRA wake-up triggers (docs/dras.md "Triggers"; ruling CLOCHE-A123). Every
// trigger funnels into notifyDRA: the event joins the DRA's inbox and the
// inbox is drained into a single wake-up when the DRA is idle and the budget
// gate is open. Nothing here starts a wake-up directly, so a burst of events
// is one wake-up and no event starts a second run.

// draRetryInterval is how often events held back by the budget gate are
// retried. A variable for tests.
var draRetryInterval = 30 * time.Second

type draTriggers struct {
	once    sync.Once
	inbox   *dra.Inbox
	mu      sync.Mutex // serialises drain: take + wake
	loaded  map[string]bool
	retries map[string]*time.Timer
}

func (s *ClocheServer) triggers() *draTriggers {
	s.draTrig.once.Do(func() {
		s.draTrig.inbox = dra.NewInbox()
		s.draTrig.loaded = map[string]bool{}
		s.draTrig.retries = map[string]*time.Timer{}
	})
	return &s.draTrig
}

func (s *ClocheServer) draInbox(projectDir string) *dra.Inbox {
	t := s.triggers()
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded[projectDir] {
		t.loaded[projectDir] = true
		if err := t.inbox.Load(projectDir); err != nil {
			log.Printf("dra triggers: loading pending events for %s: %v", projectDir, err)
		}
	}
	return t.inbox
}

// notifyDRA queues ev for DRA name and tries to deliver. It returns whether
// the event was queued (false for a duplicate ruling/heartbeat).
func (s *ClocheServer) notifyDRA(projectDir, name string, ev dra.Event) bool {
	projectDir = normalizeProjectDir(projectDir)
	queued := s.draInbox(projectDir).Add(projectDir, name, ev)
	s.drainDRA(projectDir, name)
	return queued
}

// drainDRA delivers every pending event of the DRA in one wake-up, unless it
// is awake or the budget gate is closed, in which case the events stay and
// the end of the running wake-up (or the retry timer) tries again.
func (s *ClocheServer) drainDRA(projectDir, name string) {
	t := s.triggers()
	inbox := s.draInbox(projectDir)
	t.mu.Lock()
	defer t.mu.Unlock()

	s.mu.Lock()
	_, busy := s.wakeActive[wakeKey(projectDir, name)]
	s.mu.Unlock()
	if busy || inbox.Pending(projectDir, name) == 0 {
		return
	}
	if s.budgetBlocked() {
		s.scheduleDRARetryLocked(projectDir, name)
		return
	}
	events := inbox.Take(projectDir, name)
	if len(events) == 0 {
		return
	}
	if _, err := s.WakeDRA(context.Background(), projectDir, name, dra.FormatMessage(name, events), false); err != nil {
		inbox.Requeue(projectDir, name, events)
		switch {
		case errors.Is(err, ErrBudgetBlocked):
			s.scheduleDRARetryLocked(projectDir, name)
		case errors.Is(err, ErrWakeInProgress):
			// The running wake-up drains again when it ends.
		default:
			log.Printf("dra triggers: waking %s (%d event(s) kept): %v", name, len(events), err)
		}
	}
}

func (s *ClocheServer) scheduleDRARetryLocked(projectDir, name string) {
	t := s.triggers()
	k := wakeKey(projectDir, name)
	if _, ok := t.retries[k]; ok {
		return
	}
	t.retries[k] = time.AfterFunc(draRetryInterval, func() {
		t.mu.Lock()
		delete(t.retries, k)
		t.mu.Unlock()
		s.drainDRA(projectDir, name)
	})
}

// draWakeEnded is called when a wake-up finishes: events that arrived while
// it ran are delivered now, as one message.
func (s *ClocheServer) draWakeEnded(projectDir, name string) { s.drainDRA(projectDir, name) }

// onTaskResult is the loop's callback: a task finished, failed or parked.
// The task belongs to the DRA whose name is among its labels.
func (s *ClocheServer) onTaskResult(r host.TaskResult) {
	var kind, verb string
	switch r.State {
	case domain.RunStateSucceeded:
		kind, verb = dra.KindTaskFinished, "finished"
	case domain.RunStateParked:
		kind, verb = dra.KindTaskParked, "parked"
	case domain.RunStateCancelled:
		return // stopped on request: a deliberate act, not a result
	default:
		kind, verb = dra.KindTaskFailed, "failed"
	}
	projectDir := normalizeProjectDir(r.ProjectDir)
	owner, ignored := dra.DomainOf(r.Labels, dra.ValidNames(projectDir))
	if owner == "" {
		return
	}
	if len(ignored) > 0 {
		log.Printf("dra triggers: task %s carries several DRA labels; using %q, ignoring %s", r.TaskID, owner, strings.Join(ignored, ", "))
	}
	text := "task " + verb
	if r.Title != "" {
		text = fmt.Sprintf("task %q %s", r.Title, verb)
	}
	s.notifyDRA(projectDir, owner, dra.Event{Kind: kind, Subject: r.TaskID, Text: text})
}

// notifySignalDRA tells the DRA a signal names (by its domain) about it.
// Called after the handler ran, so the task it created already exists.
func (s *ClocheServer) notifySignalDRA(projectDir string, sig signal.Signal, handlerErr error) {
	if sig.Domain == "" {
		return
	}
	projectDir = normalizeProjectDir(projectDir)
	owner, _ := dra.DomainOf([]string{sig.Domain}, dra.ValidNames(projectDir))
	if owner == "" {
		return
	}
	if sig.Source == "dra:"+owner {
		return // the DRA's own signal
	}
	text := fmt.Sprintf("%s signal: %s", sig.Severity, sig.Title)
	if handlerErr != nil {
		text += " (handle-notification failed: " + handlerErr.Error() + ")"
	}
	s.notifyDRA(projectDir, owner, dra.Event{Kind: dra.KindSignal, Subject: sig.Source + "/" + sig.Key, Text: text})
}

// NotifyDRARuling tells the DRA a ruling arrived on a Saga question it asked.
func (s *ClocheServer) NotifyDRARuling(projectDir, name, question, ruling string) error {
	if _, err := dra.Find(normalizeProjectDir(projectDir), name); err != nil {
		return err
	}
	s.notifyDRA(projectDir, name, dra.Event{Kind: dra.KindRuling, Subject: question, Text: ruling})
	return nil
}

// NotifyDRAHeartbeat wakes the DRA if it has not been woken for the
// project's heartbeat interval. Called by a poll-step timer via
// `cloche dra heartbeat`; calling it more often than the interval is
// harmless. It reports whether a heartbeat was queued.
func (s *ClocheServer) NotifyDRAHeartbeat(projectDir, name string) (bool, error) {
	projectDir = normalizeProjectDir(projectDir)
	if _, err := dra.Find(projectDir, name); err != nil {
		return false, err
	}
	interval := dra.DefaultHeartbeat
	if cfg, err := config.Load(projectDir); err == nil {
		interval = cfg.DRAs.HeartbeatInterval()
	}
	sessions, _ := dra.LoadSessions(projectDir)
	last := sessions[name].LastWakeAt
	if !dra.HeartbeatDue(last, time.Now(), interval) {
		return false, nil
	}
	return s.notifyDRA(projectDir, name, dra.Event{Kind: dra.KindHeartbeat, Text: "periodic check-in"}), nil
}

// runDRANotify handles a dra.NotifyRequest arriving over RunWorkflow.
func (s *ClocheServer) runDRANotify(req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	name := req.Env[dra.WakeEnvDRA]
	switch req.Env[dra.NotifyEnvKind] {
	case dra.KindRuling:
		if req.Env[dra.WakeEnvMessage] == "" {
			return nil, fmt.Errorf("a ruling needs text")
		}
		if err := s.NotifyDRARuling(req.ProjectDir, name, req.Env[dra.NotifyEnvSubject], req.Env[dra.WakeEnvMessage]); err != nil {
			return nil, err
		}
		return &pb.RunWorkflowResponse{}, nil
	case dra.KindHeartbeat:
		if _, err := s.NotifyDRAHeartbeat(req.ProjectDir, name); err != nil {
			return nil, err
		}
		return &pb.RunWorkflowResponse{}, nil
	}
	return nil, fmt.Errorf("unknown DRA notification kind %q", req.Env[dra.NotifyEnvKind])
}
