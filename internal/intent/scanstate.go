package intent

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// RepoScanState is the incremental-scan cursor set for one source tree: the
// last git commit mined, the run IDs already mined (bounded window, pruned
// by the scan), and a content hash per doc path already mined, so a fresh
// clone or a re-run doesn't re-mine everything and produce duplicate
// requirements.
type RepoScanState struct {
	LastCommit  string            `yaml:"last_commit"`
	ScannedRuns []string          `yaml:"scanned_runs"`
	ScannedDocs map[string]string `yaml:"scanned_docs"`
}

// ScanState records the incremental-scan cursors persisted in
// .cloche/intent/scan-state.yaml. Repos holds one RepoScanState per source
// tree Collect walks, keyed by the repo's [[repositories]] name — or "" for
// the project root itself, the only entry in a project with no configured
// repositories.
//
// LastScanAt records when intent-scan last completed (UTC), so consumers
// (e.g. the dashboard's new-since-scan badge) can tell which requirements
// were created by the most recent scan. Zero means no scan has run yet.
//
// LastScanStats records the per-repo collection counts from the most recent
// collect-sources pass (root plus every configured repo), so the
// Requirements view and `cloche intent scan` can show a thin scan is thin
// instead of looking identical to a rich one.
type ScanState struct {
	Repos         map[string]*RepoScanState `yaml:"repos,omitempty"`
	LastScanAt    time.Time                 `yaml:"last_scan_at,omitempty"`
	LastScanStats ScanStats                 `yaml:"last_scan_stats,omitempty"`

	// Deprecated: the single-cursor fields written before multi-repo
	// support. Only ever populated by unmarshaling a pre-migration
	// scan-state.yaml; migrateLegacy folds them into Repos[""] and clears
	// them, so they are always empty after LoadScanState returns or a fresh
	// state is saved.
	LastCommit  string            `yaml:"last_commit,omitempty"`
	ScannedRuns []string          `yaml:"scanned_runs,omitempty"`
	ScannedDocs map[string]string `yaml:"scanned_docs,omitempty"`
}

// migrateLegacy folds a pre-multi-repo single-cursor scan-state.yaml into
// Repos[""] (the project root's cursor) and clears the deprecated fields, so
// every other reader can treat Repos as the sole source of truth. A no-op on
// an already-migrated or fresh state.
func (st *ScanState) migrateLegacy() {
	if st.Repos == nil {
		st.Repos = map[string]*RepoScanState{}
	}
	if st.LastCommit != "" || len(st.ScannedRuns) > 0 || len(st.ScannedDocs) > 0 {
		if _, ok := st.Repos[""]; !ok {
			st.Repos[""] = &RepoScanState{
				LastCommit:  st.LastCommit,
				ScannedRuns: st.ScannedRuns,
				ScannedDocs: st.ScannedDocs,
			}
		}
		st.LastCommit = ""
		st.ScannedRuns = nil
		st.ScannedDocs = nil
	}
}

// LoadScanState reads scan-state.yaml. A missing file yields a zero-value
// ScanState (empty cursors), not an error — the first scan of a project
// starts from scratch. A pre-multi-repo scan-state.yaml is transparently
// migrated (see migrateLegacy) before it's returned.
func (s *Store) LoadScanState() (*ScanState, error) {
	data, err := os.ReadFile(s.scanStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &ScanState{Repos: map[string]*RepoScanState{}}, nil
		}
		return nil, fmt.Errorf("intent: reading scan-state.yaml: %w", err)
	}

	var st ScanState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("intent: parsing scan-state.yaml: %w", err)
	}
	st.migrateLegacy()
	return &st, nil
}

// SaveScanState writes scan-state.yaml, creating the intent/ directory if needed.
func (s *Store) SaveScanState(st *ScanState) error {
	if err := os.MkdirAll(s.IntentDir(), 0o755); err != nil {
		return fmt.Errorf("intent: creating intent dir: %w", err)
	}

	data, err := yaml.Marshal(st)
	if err != nil {
		return fmt.Errorf("intent: marshaling scan-state.yaml: %w", err)
	}
	if err := os.WriteFile(s.scanStatePath(), data, 0o644); err != nil {
		return fmt.Errorf("intent: writing scan-state.yaml: %w", err)
	}
	return nil
}
