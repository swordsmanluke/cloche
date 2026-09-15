package attention

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// muteFilePath returns the path of the file recording muted attention items
// for a project. Persisted per-project (like the loop-stopped flag) so a
// mute survives daemon restarts.
func muteFilePath(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", ".attention-mutes.json")
}

// loadMutedKeys reads the set of muted item keys for a project. A missing or
// unreadable file is treated as "nothing muted" rather than an error, since
// muting is a best-effort UI convenience, not load-bearing state.
func loadMutedKeys(projectDir string) map[string]bool {
	data, err := os.ReadFile(muteFilePath(projectDir))
	if err != nil {
		return map[string]bool{}
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return map[string]bool{}
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

func saveMutedKeys(projectDir string, set map[string]bool) error {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(muteFilePath(projectDir)), 0755); err != nil {
		return err
	}
	return os.WriteFile(muteFilePath(projectDir), data, 0644)
}

// Mute suppresses the attention item identified by key (see Item.Key) from
// future Compute results for projectDir, until Unmute is called.
func Mute(projectDir, key string) error {
	set := loadMutedKeys(projectDir)
	set[key] = true
	return saveMutedKeys(projectDir, set)
}

// Unmute reverses a prior Mute call. Muting a key that was never muted, or
// unmuting one that isn't muted, is not an error.
func Unmute(projectDir, key string) error {
	set := loadMutedKeys(projectDir)
	if !set[key] {
		return nil
	}
	delete(set, key)
	return saveMutedKeys(projectDir, set)
}

// IsMuted reports whether key is currently muted for projectDir.
func IsMuted(projectDir, key string) bool {
	return loadMutedKeys(projectDir)[key]
}
