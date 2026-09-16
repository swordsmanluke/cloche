package intent

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ScanState records the incremental-scan cursors persisted in
// .cloche/intent/scan-state.yaml: the last git commit mined, the run IDs
// already mined (bounded window, pruned by the scan), and a content hash per
// doc path already mined, so a fresh clone or a re-run doesn't re-mine
// everything and produce duplicate requirements.
type ScanState struct {
	LastCommit  string            `yaml:"last_commit"`
	ScannedRuns []string          `yaml:"scanned_runs"`
	ScannedDocs map[string]string `yaml:"scanned_docs"`
	// LastScanAt records when intent-scan last completed (UTC), so consumers
	// (e.g. the dashboard's new-since-scan badge) can tell which requirements
	// were created by the most recent scan. Zero means no scan has run yet.
	LastScanAt time.Time `yaml:"last_scan_at,omitempty"`
	// Repos holds the collection cursors for each configured
	// [[repositories]] entry collect-sources has descended into, keyed by
	// the repository's configured name. The project root's own cursors stay
	// in the fields above, unchanged from before repos existed, so a
	// scan-state.yaml from a project with no configured repos round-trips
	// exactly as it always has.
	Repos map[string]*RepoCursor `yaml:"repos,omitempty"`
	// LastScanStats records the per-repo collection counts from the most
	// recent collect-sources pass (root plus every configured repo), so the
	// Requirements view and `cloche intent scan` can show a thin scan is
	// thin instead of looking identical to a rich one.
	LastScanStats ScanStats `yaml:"last_scan_stats,omitempty"`
}

// RepoCursor holds the same three collection cursors as ScanState's
// top-level fields, scoped to one configured [[repositories]] entry.
type RepoCursor struct {
	LastCommit  string            `yaml:"last_commit,omitempty"`
	ScannedRuns []string          `yaml:"scanned_runs,omitempty"`
	ScannedDocs map[string]string `yaml:"scanned_docs,omitempty"`
}

// LoadScanState reads scan-state.yaml. A missing file yields a zero-value
// ScanState (empty cursors), not an error — the first scan of a project
// starts from scratch.
func (s *Store) LoadScanState() (*ScanState, error) {
	data, err := os.ReadFile(s.scanStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &ScanState{}, nil
		}
		return nil, fmt.Errorf("intent: reading scan-state.yaml: %w", err)
	}

	var st ScanState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("intent: parsing scan-state.yaml: %w", err)
	}
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
