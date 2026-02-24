package evolution

import (
	"sync"
	"time"
)

// TriggerConfig configures the evolution trigger.
type TriggerConfig struct {
	DebounceSeconds int
	RunFunc         func(projectDir, workflowName, runID string)
}

// Trigger debounces evolution pipeline runs per project+workflow key.
type Trigger struct {
	cfg    TriggerConfig
	mu     sync.Mutex
	timers map[string]*time.Timer
}

// NewTrigger creates a new evolution trigger.
func NewTrigger(cfg TriggerConfig) *Trigger {
	if cfg.DebounceSeconds <= 0 {
		cfg.DebounceSeconds = 30
	}
	return &Trigger{
		cfg:    cfg,
		timers: make(map[string]*time.Timer),
	}
}

// Fire schedules an evolution run for the given project+workflow.
// If a run is already pending for this key, it resets the debounce timer
// and uses the latest runID.
func (t *Trigger) Fire(projectDir, workflowName, runID string) {
	key := projectDir + ":" + workflowName

	t.mu.Lock()
	defer t.mu.Unlock()

	if timer, ok := t.timers[key]; ok {
		timer.Stop()
	}

	t.timers[key] = time.AfterFunc(time.Duration(t.cfg.DebounceSeconds)*time.Second, func() {
		t.mu.Lock()
		delete(t.timers, key)
		t.mu.Unlock()

		t.cfg.RunFunc(projectDir, workflowName, runID)
	})
}

// Stop cancels all pending timers.
func (t *Trigger) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, timer := range t.timers {
		timer.Stop()
		delete(t.timers, key)
	}
}
