package intent

import (
	"fmt"
	"os"

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
