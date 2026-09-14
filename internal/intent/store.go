package intent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// maxIDAttempts bounds how many random IDs CreateRequirement will try before
// giving up when every generated ID collides with an existing file.
const maxIDAttempts = 50

// Store reads and writes a project's .cloche/intent/ directory: requirement
// files, the domain map, and scan-state cursors. It is the sole concrete
// backend for intent data — files are the source of truth, and Store keeps a
// small mtime-keyed in-memory cache so repeated reads within a process (e.g.
// selection during a run) don't re-parse unchanged files.
//
// A project with no .cloche/intent/ directory is fully supported: every
// read method returns an empty result and no error (the dormancy guarantee),
// so callers never need to special-case a project that hasn't run a scan yet.
type Store struct {
	dir string // project root, not .cloche/intent/ itself

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	modTime time.Time
	req     *Requirement
}

// NewStore creates a Store rooted at the given project directory.
func NewStore(projectDir string) *Store {
	return &Store{dir: projectDir, cache: make(map[string]cacheEntry)}
}

// IntentDir returns the project's .cloche/intent/ directory.
func (s *Store) IntentDir() string {
	return filepath.Join(s.dir, ".cloche", "intent")
}

func (s *Store) requirementsDir() string {
	return filepath.Join(s.IntentDir(), "requirements")
}

func (s *Store) domainsPath() string {
	return filepath.Join(s.IntentDir(), "domains.yaml")
}

func (s *Store) scanStatePath() string {
	return filepath.Join(s.IntentDir(), "scan-state.yaml")
}

// ListRequirements returns every requirement file under requirements/,
// sorted by ID. A missing requirements/ (or intent/) directory yields an
// empty, non-error result.
func (s *Store) ListRequirements() ([]*Requirement, error) {
	entries, err := os.ReadDir(s.requirementsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("intent: reading requirements dir: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var reqs []*Requirement
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(s.requirementsDir(), e.Name())
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("intent: stat %s: %w", e.Name(), err)
		}
		if cached, ok := s.cache[path]; ok && cached.modTime.Equal(info.ModTime()) {
			reqs = append(reqs, cached.req)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("intent: reading %s: %w", e.Name(), err)
		}
		req, err := ParseRequirement(data)
		if err != nil {
			return nil, fmt.Errorf("intent: %s: %w", e.Name(), err)
		}
		s.cache[path] = cacheEntry{modTime: info.ModTime(), req: req}
		reqs = append(reqs, req)
	}

	sort.Slice(reqs, func(i, j int) bool { return reqs[i].ID < reqs[j].ID })
	return reqs, nil
}

// GetRequirement loads a single requirement by ID.
func (s *Store) GetRequirement(id string) (*Requirement, error) {
	path := filepath.Join(s.requirementsDir(), id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("intent: requirement %s not found", id)
		}
		return nil, fmt.Errorf("intent: reading %s: %w", id, err)
	}
	req, err := ParseRequirement(data)
	if err != nil {
		return nil, fmt.Errorf("intent: %s: %w", id, err)
	}

	if info, err := os.Stat(path); err == nil {
		s.mu.Lock()
		s.cache[path] = cacheEntry{modTime: info.ModTime(), req: req}
		s.mu.Unlock()
	}
	return req, nil
}

// SaveRequirement writes req to its file (requirements/<id>.md), creating
// the requirements/ directory if needed. req.ID must already be set; use
// CreateRequirement to allocate a new ID.
func (s *Store) SaveRequirement(req *Requirement) error {
	if req.ID == "" {
		return fmt.Errorf("intent: cannot save a requirement without an id")
	}
	if err := os.MkdirAll(s.requirementsDir(), 0o755); err != nil {
		return fmt.Errorf("intent: creating requirements dir: %w", err)
	}

	data, err := MarshalRequirement(req)
	if err != nil {
		return fmt.Errorf("intent: marshaling %s: %w", req.ID, err)
	}

	path := filepath.Join(s.requirementsDir(), req.ID+".md")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("intent: writing %s: %w", req.ID, err)
	}

	if info, err := os.Stat(path); err == nil {
		s.mu.Lock()
		s.cache[path] = cacheEntry{modTime: info.ModTime(), req: req}
		s.mu.Unlock()
	}
	return nil
}

// CreateRequirement allocates a fresh, collision-checked ID for req (which
// must not already have one), stamps Created/Updated, and saves it.
func (s *Store) CreateRequirement(req *Requirement) (*Requirement, error) {
	if req.ID != "" {
		return nil, fmt.Errorf("intent: new requirement must not have an id set")
	}

	id, err := s.allocateID()
	if err != nil {
		return nil, err
	}
	req.ID = id

	now := time.Now().UTC()
	if req.Created.IsZero() {
		req.Created = now
	}
	req.Updated = now

	if err := s.SaveRequirement(req); err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Store) allocateID() (string, error) {
	exists := func(id string) bool {
		_, err := os.Stat(filepath.Join(s.requirementsDir(), id+".md"))
		return err == nil
	}
	return allocateUniqueID(GenerateRequirementID, exists, maxIDAttempts)
}

// allocateUniqueID calls generate up to maxAttempts times, returning the
// first result for which exists reports false. It is split out from
// allocateID so the retry/exhaustion behavior can be tested without touching
// the filesystem or relying on an actual ID collision occurring.
func allocateUniqueID(generate func() string, exists func(string) bool, maxAttempts int) (string, error) {
	for i := 0; i < maxAttempts; i++ {
		id := generate()
		if !exists(id) {
			return id, nil
		}
	}
	return "", fmt.Errorf("intent: could not allocate a unique requirement id after %d attempts", maxAttempts)
}

// LoadDomains reads domains.yaml. A missing file yields an empty DomainMap
// (version 1, no domains), not an error.
func (s *Store) LoadDomains() (*DomainMap, error) {
	data, err := os.ReadFile(s.domainsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &DomainMap{Version: 1}, nil
		}
		return nil, fmt.Errorf("intent: reading domains.yaml: %w", err)
	}

	var dm DomainMap
	if err := yaml.Unmarshal(data, &dm); err != nil {
		return nil, fmt.Errorf("intent: parsing domains.yaml: %w", err)
	}
	return &dm, nil
}

// SaveDomains writes domains.yaml, creating the intent/ directory if needed.
func (s *Store) SaveDomains(dm *DomainMap) error {
	if err := os.MkdirAll(s.IntentDir(), 0o755); err != nil {
		return fmt.Errorf("intent: creating intent dir: %w", err)
	}

	data, err := yaml.Marshal(dm)
	if err != nil {
		return fmt.Errorf("intent: marshaling domains.yaml: %w", err)
	}
	if err := os.WriteFile(s.domainsPath(), data, 0o644); err != nil {
		return fmt.Errorf("intent: writing domains.yaml: %w", err)
	}
	return nil
}
