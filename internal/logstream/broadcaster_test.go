package logstream

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcaster_SubscribePublish(t *testing.T) {
	b := NewBroadcaster()

	sub := b.Subscribe("run-1")

	line := LogLine{Timestamp: "2026-03-03T10:00:00Z", Type: "status", Content: "step_started: build"}
	b.Publish("run-1", line)

	select {
	case received := <-sub.C:
		assert.Equal(t, line, received)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log line")
	}
}

func TestBroadcaster_MultipleSubscribers(t *testing.T) {
	b := NewBroadcaster()

	sub1 := b.Subscribe("run-1")
	sub2 := b.Subscribe("run-1")

	line := LogLine{Timestamp: "2026-03-03T10:00:00Z", Type: "script", Content: "hello"}
	b.Publish("run-1", line)

	for _, sub := range []*Subscriber{sub1, sub2} {
		select {
		case received := <-sub.C:
			assert.Equal(t, line, received)
		case <-time.After(time.Second):
			t.Fatal("timed out")
		}
	}
}

func TestBroadcaster_IsolatedRuns(t *testing.T) {
	b := NewBroadcaster()

	sub1 := b.Subscribe("run-1")
	sub2 := b.Subscribe("run-2")

	b.Publish("run-1", LogLine{Content: "for run-1"})
	b.Publish("run-2", LogLine{Content: "for run-2"})

	r1 := <-sub1.C
	assert.Equal(t, "for run-1", r1.Content)

	r2 := <-sub2.C
	assert.Equal(t, "for run-2", r2.Content)
}

func TestBroadcaster_Unsubscribe(t *testing.T) {
	b := NewBroadcaster()

	sub := b.Subscribe("run-1")
	b.Unsubscribe("run-1", sub)

	// Channel should be closed
	_, ok := <-sub.C
	assert.False(t, ok, "channel should be closed after unsubscribe")
}

func TestBroadcaster_Finish(t *testing.T) {
	b := NewBroadcaster()

	sub := b.Subscribe("run-1")
	b.Finish("run-1")

	// Channel should be closed
	_, ok := <-sub.C
	assert.False(t, ok, "channel should be closed after finish")
}

func TestBroadcaster_SubscribeAfterFinish(t *testing.T) {
	b := NewBroadcaster()

	b.Subscribe("run-1")
	b.Finish("run-1")

	// Subscribe after finish returns closed channel
	sub := b.Subscribe("run-1")
	_, ok := <-sub.C
	assert.False(t, ok, "channel should be closed for finished run")
}

func TestBroadcaster_IsActive(t *testing.T) {
	b := NewBroadcaster()

	assert.False(t, b.IsActive("run-1"), "unknown run should not be active")

	b.Subscribe("run-1")
	assert.True(t, b.IsActive("run-1"), "run with subscriber should be active")

	b.Finish("run-1")
	assert.False(t, b.IsActive("run-1"), "finished run should not be active")
}

func TestBroadcaster_PublishNoSubscribers(t *testing.T) {
	b := NewBroadcaster()

	// Should not panic
	b.Publish("nonexistent", LogLine{Content: "nobody listening"})
}

func TestBroadcaster_ConcurrentAccess(t *testing.T) {
	b := NewBroadcaster()

	var wg sync.WaitGroup

	// Concurrent subscribers
	subs := make([]*Subscriber, 10)
	for i := 0; i < 10; i++ {
		subs[i] = b.Subscribe("run-1")
	}

	// Concurrent publishers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				b.Publish("run-1", LogLine{Content: "msg"})
			}
		}(i)
	}

	wg.Wait()

	// Drain and finish
	b.Finish("run-1")

	// All subscriber channels should be closed
	for _, sub := range subs {
		// Drain remaining
		for range sub.C {
		}
	}
}

func TestBroadcaster_FinishNonexistent(t *testing.T) {
	b := NewBroadcaster()
	// Should not panic
	b.Finish("nonexistent")
}

func TestBroadcaster_UnsubscribeNonexistent(t *testing.T) {
	b := NewBroadcaster()
	sub := newSubscriber(1)
	// Should not panic
	b.Unsubscribe("nonexistent", sub)
}

func TestBroadcaster_LogLineStepName(t *testing.T) {
	b := NewBroadcaster()
	sub := b.Subscribe("run-1")

	line := LogLine{
		Timestamp: "2026-03-10T10:00:00Z",
		Type:      "llm",
		Content:   "Analyzing code...",
		StepName:  "implement",
	}
	b.Publish("run-1", line)

	select {
	case received := <-sub.C:
		assert.Equal(t, "implement", received.StepName)
		assert.Equal(t, "Analyzing code...", received.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log line")
	}
}

func TestBroadcaster_SlowSubscriberDropsMessages(t *testing.T) {
	b := NewBroadcaster()

	sub := b.Subscribe("run-1")

	// Fill the buffer (256 items)
	for i := 0; i < 300; i++ {
		b.Publish("run-1", LogLine{Content: "msg"})
	}

	// Should have received up to buffer size (256)
	count := 0
	for {
		select {
		case _, ok := <-sub.C:
			if !ok {
				goto done
			}
			count++
		default:
			goto done
		}
	}
done:
	require.Equal(t, 256, count, "should have received exactly buffer size messages")
}
