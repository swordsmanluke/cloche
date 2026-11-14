package dra

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// WaitsFile is the daemon's record of each project's pending DRA waits, next
// to the sessions file under the project's gitignored .cloche/runs/.
const WaitsFile = "dra-waits.json"

// WaitsPath is the waits file of a project.
func WaitsPath(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", "runs", WaitsFile)
}

// FileWaitStore keeps pending waits in one file per project, so they survive a
// daemon restart. Known lists the projects whose files to read at start (the
// projects the daemon has seen); projects that save a wait are remembered too.
type FileWaitStore struct {
	mu       sync.Mutex
	known    func() []string
	projects map[string]bool
}

// NewFileWaitStore returns a store. known may be nil.
func NewFileWaitStore(known func() []string) *FileWaitStore {
	return &FileWaitStore{known: known, projects: map[string]bool{}}
}

func (s *FileWaitStore) projectDirs() []string {
	set := map[string]bool{}
	for p := range s.projects {
		set[p] = true
	}
	if s.known != nil {
		for _, p := range s.known() {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func readWaits(project string) ([]Wait, error) {
	data, err := os.ReadFile(WaitsPath(project))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ws []Wait
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("reading %s: %w", WaitsPath(project), err)
	}
	return ws, nil
}

func writeWaits(project string, ws []Wait) error {
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return err
	}
	path := WaitsPath(project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Save inserts or replaces a wait.
func (s *FileWaitStore) Save(w Wait) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, err := readWaits(w.Project)
	if err != nil {
		return err
	}
	replaced := false
	for i := range ws {
		if ws[i].ID == w.ID {
			ws[i], replaced = w, true
		}
	}
	if !replaced {
		ws = append(ws, w)
	}
	if err := writeWaits(w.Project, ws); err != nil {
		return err
	}
	s.projects[w.Project] = true
	return nil
}

// Delete removes a wait.
func (s *FileWaitStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projectDirs() {
		ws, err := readWaits(p)
		if err != nil {
			return err
		}
		for i := range ws {
			if ws[i].ID == id {
				return writeWaits(p, append(ws[:i], ws[i+1:]...))
			}
		}
	}
	return nil
}

// All returns every wait of every known project, oldest first. A project whose
// file cannot be read is an error: silently dropping its waits would lose them
// at the next save.
func (s *FileWaitStore) All() ([]Wait, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Wait
	for _, p := range s.projectDirs() {
		ws, err := readWaits(p)
		if err != nil {
			return nil, err
		}
		out = append(out, ws...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
