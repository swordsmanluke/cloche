package grpc

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/swordsmanluke/cloche/internal/dra"
)

// DRA waits (docs/dras.md "Waits"). A DRA registers a wait on a named poll
// and ends its wake-up; when the poll reports a result, fails, or the wait
// times out, one wait event joins the DRA's inbox through notifyDRA, so it is
// batched under the same rules as every other trigger.

// draWaitTick is how often pending waits are checked; each wait's own
// interval (at least a minute) decides whether it is actually polled. A
// variable for tests.
var draWaitTick = 30 * time.Second

type draWaitState struct {
	once sync.Once
	reg  *dra.WaitRegistry
}

// waitRegistry returns the server's wait registry, creating it (and its tick
// loop) on first use.
// Waits live in per-project files (dra.FileWaitStore) so they survive a daemon
// restart, and each poll is the recorded host run dra-wait-<name>. Calling it
// at daemon start (ResumeDRAWaits) resumes polling every pending wait.
func (s *ClocheServer) waitRegistry() *dra.WaitRegistry {
	s.draWaits.once.Do(func() {
		store := dra.NewFileWaitStore(func() []string {
			projects, err := s.store.ListProjects(context.Background())
			if err != nil {
				log.Printf("dra waits: listing projects: %v", err)
			}
			return projects
		})
		s.draWaits.reg = dra.NewWaitRegistry(store, &recordedPollRunner{s: s},
			func(project, name string, e dra.Event) { s.notifyDRA(project, name, e) })
		go func() {
			if err := s.draWaits.reg.Tick(context.Background()); err != nil {
				log.Printf("dra waits: %v", err)
			}
			for range time.Tick(draWaitTick) {
				if err := s.draWaits.reg.Tick(context.Background()); err != nil {
					log.Printf("dra waits: %v", err)
				}
			}
		}()
	})
	return s.draWaits.reg
}

// RegisterDRAWait registers a wait for the named DRA of a project.
func (s *ClocheServer) RegisterDRAWait(_ context.Context, projectDir, name string, req dra.WaitRequest) (string, error) {
	projectDir = normalizeProjectDir(projectDir)
	if _, err := dra.Find(projectDir, name); err != nil {
		return "", err
	}
	return s.waitRegistry().Register(projectDir, name, req)
}

// ListDRAWaits lists the DRA's own pending waits.
func (s *ClocheServer) ListDRAWaits(_ context.Context, projectDir, name string) ([]dra.Wait, error) {
	return s.waitRegistry().List(normalizeProjectDir(projectDir), name)
}

// CancelDRAWait cancels one of the DRA's own pending waits.
func (s *ClocheServer) CancelDRAWait(_ context.Context, projectDir, name, id string) error {
	return s.waitRegistry().Cancel(normalizeProjectDir(projectDir), name, id)
}

// ResumeDRAWaits starts polling the waits that were pending when the daemon
// last stopped. The daemon calls it at start.
func (s *ClocheServer) ResumeDRAWaits() { s.waitRegistry() }
