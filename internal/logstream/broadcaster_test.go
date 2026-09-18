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

func TestBroadcaster_GetHistory(t *testing.T) {
	b := NewBroadcaster()

	// Unknown run returns nil.
	assert.Nil(t, b.GetHistory("unknown"))

	b.Start("run-1")
	assert.Nil(t, b.GetHistory("run-1"), "empty history should return nil")

	lines := []LogLine{
		{Timestamp: "2026-04-28T10:00:00Z", Type: "status", Content: "step_started: build", StepName: "build"},
		{Timestamp: "2026-04-28T10:00:01Z", Type: "llm", Content: "Compiling...", StepName: "build"},
		{Timestamp: "2026-04-28T10:00:02Z", Type: "llm", Content: "Done.", StepName: "build"},
	}
	for _, l := range lines {
		b.Publish("run-1", l)
	}

	got := b.GetHistory("run-1")
	require.Equal(t, lines, got)

	// GetHistory returns a copy; mutating it doesn't affect the broadcaster.
	got[0].Content = "mutated"
	got2 := b.GetHistory("run-1")
	assert.Equal(t, "step_started: build", got2[0].Content)
}

func TestBroadcaster_GetHistoryAfterFinish(t *testing.T) {
	b := NewBroadcaster()
	b.Start("run-1")
	b.Publish("run-1", LogLine{Type: "llm", Content: "hello"})

	b.Finish("run-1")

	// After Finish, history is cleared.
	assert.Nil(t, b.GetHistory("run-1"))
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

func TestBroadcaster_LogLineRunID(t *testing.T) {
	b := NewBroadcaster()
	sub := b.Subscribe("run-1")

	line := LogLine{
		Timestamp: "2026-03-10T10:00:00Z",
		Type:      "llm",
		Content:   "Analyzing code...",
		StepName:  "implement",
		RunID:     "run-1",
	}
	b.Publish("run-1", line)

	select {
	case received := <-sub.C:
		assert.Equal(t, "run-1", received.RunID)
		assert.Equal(t, "implement", received.StepName)
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

func TestBroadcaster_HistoryCapEvictsOldest(t *testing.T) {
	b := NewBroadcasterWithHistoryCap(3)
	b.Start("run-1")

	for i := 0; i < 5; i++ {
		b.Publish("run-1", LogLine{Content: string(rune('a' + i))})
	}

	got := b.GetHistory("run-1")
	require.Len(t, got, 3, "history should be capped at 3")
	assert.Equal(t, "c", got[0].Content, "oldest retained line should be the 3rd published (0-indexed seq 2)")
	assert.Equal(t, "e", got[2].Content)
}

func TestBroadcaster_NewBroadcasterWithHistoryCapDefaultsWhenNonPositive(t *testing.T) {
	b := NewBroadcasterWithHistoryCap(0)
	assert.Equal(t, DefaultHistoryCap, b.historyCap)

	b2 := NewBroadcasterWithHistoryCap(-5)
	assert.Equal(t, DefaultHistoryCap, b2.historyCap)
}

func TestBroadcaster_SubscribeFromSeq_FromStart(t *testing.T) {
	b := NewBroadcaster()
	b.Start("run-1")
	b.Publish("run-1", LogLine{Content: "first"})
	b.Publish("run-1", LogLine{Content: "second"})

	sub, lines, baseSeq, ok := b.SubscribeFromSeq("run-1", -1)
	defer b.Unsubscribe("run-1", sub)

	assert.True(t, ok, "nothing evicted yet, should be gap-free")
	assert.Equal(t, 0, baseSeq)
	require.Len(t, lines, 2)
	assert.Equal(t, "first", lines[0].Content)
	assert.Equal(t, "second", lines[1].Content)

	b.Publish("run-1", LogLine{Content: "third"})
	select {
	case l := <-sub.C:
		assert.Equal(t, "third", l.Content, "line published after subscribing should arrive on the channel, continuing the same sequence")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live line")
	}
}

func TestBroadcaster_SubscribeFromSeq_ResumesAfterOffset(t *testing.T) {
	b := NewBroadcaster()
	b.Start("run-1")
	b.Publish("run-1", LogLine{Content: "a"}) // seq 0
	b.Publish("run-1", LogLine{Content: "b"}) // seq 1
	b.Publish("run-1", LogLine{Content: "c"}) // seq 2

	// Client already has seq 0 (afterSeq=0) — resume should return only
	// what's newer, not replay everything (the bug this fixes: a
	// reconnecting client used to receive the whole history again).
	sub, lines, baseSeq, ok := b.SubscribeFromSeq("run-1", 0)
	defer b.Unsubscribe("run-1", sub)

	assert.True(t, ok)
	assert.Equal(t, 1, baseSeq)
	require.Len(t, lines, 2)
	assert.Equal(t, "b", lines[0].Content)
	assert.Equal(t, "c", lines[1].Content)
}

func TestBroadcaster_SubscribeFromSeq_NotGapFreeAfterEviction(t *testing.T) {
	b := NewBroadcasterWithHistoryCap(1)
	b.Start("run-1")
	b.Publish("run-1", LogLine{Content: "a"}) // seq 0 — client already has this
	b.Publish("run-1", LogLine{Content: "b"}) // seq 1 — evicted below before the client ever sees it
	b.Publish("run-1", LogLine{Content: "c"}) // seq 2, evicts seq 1 too (cap 1 retains only the newest)

	// The client's last-seen seq (0) means it wants seq 1 onward, but seq 1
	// ("b") has already fallen out of the retained window: resume can't be
	// gap-free.
	sub, lines, baseSeq, ok := b.SubscribeFromSeq("run-1", 0)
	defer b.Unsubscribe("run-1", sub)

	assert.False(t, ok, "the line after afterSeq has been evicted, resume must report not-gap-free")
	assert.Equal(t, 2, baseSeq, "should still return whatever the retained window currently holds")
	require.Len(t, lines, 1)
	assert.Equal(t, "c", lines[0].Content)
}

func TestBroadcaster_SubscribeFromSeq_UnknownRunIsGapFreeAndEmpty(t *testing.T) {
	b := NewBroadcaster()

	sub, lines, baseSeq, ok := b.SubscribeFromSeq("never-started", -1)
	defer b.Unsubscribe("never-started", sub)

	assert.True(t, ok)
	assert.Equal(t, 0, baseSeq)
	assert.Empty(t, lines)
}

func TestBroadcaster_SubscribeFromSeq_FinishedRunReturnsClosedChannel(t *testing.T) {
	b := NewBroadcaster()
	b.Start("run-1")
	b.Publish("run-1", LogLine{Content: "a"})
	b.Finish("run-1")

	sub, lines, _, ok := b.SubscribeFromSeq("run-1", -1)
	assert.True(t, ok)
	assert.Empty(t, lines, "Finish clears history to free memory; caller falls back to full.log")

	_, chOk := <-sub.C
	assert.False(t, chOk, "finished run should hand back a closed channel")
}
