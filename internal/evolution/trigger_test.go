package evolution

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTriggerDebounce(t *testing.T) {
	var count atomic.Int32
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 1,
		RunFunc: func(projectDir, workflowName, runID string) {
			count.Add(1)
		},
	})
	defer trigger.Stop()

	// Fire 3 events rapidly for the same project+workflow
	trigger.Fire("/project", "develop", "run-1")
	trigger.Fire("/project", "develop", "run-2")
	trigger.Fire("/project", "develop", "run-3")

	// Wait for debounce to fire
	time.Sleep(2 * time.Second)

	// Should have fired only once (debounced)
	assert.Equal(t, int32(1), count.Load())
}

func TestTriggerDifferentProjectsRunIndependently(t *testing.T) {
	var count atomic.Int32
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 1,
		RunFunc: func(projectDir, workflowName, runID string) {
			count.Add(1)
		},
	})
	defer trigger.Stop()

	trigger.Fire("/project-a", "develop", "run-1")
	trigger.Fire("/project-b", "develop", "run-2")

	time.Sleep(2 * time.Second)

	// Two different projects = two independent triggers
	assert.Equal(t, int32(2), count.Load())
}

func TestTriggerUsesLatestRunID(t *testing.T) {
	var lastRunID string
	var mu atomic.Value
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 1,
		RunFunc: func(projectDir, workflowName, runID string) {
			mu.Store(runID)
		},
	})
	defer trigger.Stop()

	trigger.Fire("/project", "develop", "run-1")
	trigger.Fire("/project", "develop", "run-2")
	trigger.Fire("/project", "develop", "run-3")

	time.Sleep(2 * time.Second)

	lastRunID = mu.Load().(string)
	assert.Equal(t, "run-3", lastRunID)
}

func TestTriggerStopCancelsPending(t *testing.T) {
	var count atomic.Int32
	trigger := NewTrigger(TriggerConfig{
		DebounceSeconds: 2,
		RunFunc: func(projectDir, workflowName, runID string) {
			count.Add(1)
		},
	})

	trigger.Fire("/project", "develop", "run-1")
	trigger.Stop()

	time.Sleep(3 * time.Second)

	// Should not have fired since we stopped before debounce
	assert.Equal(t, int32(0), count.Load())
}
