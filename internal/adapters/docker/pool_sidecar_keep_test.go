package docker

import (
	"context"
	"testing"

	"github.com/swordsmanluke/cloche/internal/ports"
)

type tearRuntime struct {
	ports.ContainerRuntime
	torn    []string
	stopped []string
}

func (t *tearRuntime) TeardownSidecar(_ context.Context, id string) { t.torn = append(t.torn, id) }
func (t *tearRuntime) Stop(_ context.Context, id string) error {
	t.stopped = append(t.stopped, id)
	return nil
}

func TestCleanupAttemptKeepContainerTearsDownSidecar(t *testing.T) {
	rt := &tearRuntime{}
	p := NewContainerPool(rt)
	p.mu.Lock()
	p.attempts["a1"] = &poolEntry{sessions: []*ContainerSession{{ContainerID: "c1"}}}
	p.mu.Unlock()
	if err := p.CleanupAttempt(context.Background(), "a1", true, false); err != nil {
		t.Fatal(err)
	}
	if len(rt.torn) != 1 || rt.torn[0] != "c1" || len(rt.stopped) != 0 {
		t.Fatalf("torn=%v stopped=%v", rt.torn, rt.stopped)
	}
}
