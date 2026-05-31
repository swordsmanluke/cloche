package docker

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// CredentialStager holds the per-container staging directory and fsnotify watcher
// that keep ~/.claude/.credentials.json current inside long-running containers.
//
// When the host OAuth token is atomically refreshed (rename), the watcher re-copies
// the file into the staging directory in-place (same inode), so the container sees
// the new credentials without requiring a restart.
type CredentialStager struct {
	// StagingDir is the host-side directory bind-mounted into the container at
	// /home/agent/.claude/. Exported so tests can inspect its contents.
	StagingDir string

	watcher   *fsnotify.Watcher
	claudeDir string
}

// NewCredentialStager creates a per-container staging directory, copies the Claude
// auth files from claudeDir into it, and starts an fsnotify watcher on claudeDir to
// re-copy .credentials.json on Write or Create events.
//
// On watcher startup failure, a warning is logged and the returned stager has no
// watcher: credentials are staged once but not automatically refreshed.
// The caller must call Close() when the associated container stops.
func NewCredentialStager(containerID, claudeDir string) (*CredentialStager, error) {
	stagingDir, err := os.MkdirTemp("", "cloche-claude-")
	if err != nil {
		return nil, fmt.Errorf("creating credential staging dir: %w", err)
	}

	if err := copyCredsToStaging(claudeDir, stagingDir); err != nil {
		os.RemoveAll(stagingDir)
		return nil, fmt.Errorf("staging credentials: %w", err)
	}

	s := &CredentialStager{StagingDir: stagingDir, claudeDir: claudeDir}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("runtime: container %s: warning: could not create fsnotify watcher: %v", containerID, err)
		return s, nil
	}

	if err := watcher.Add(claudeDir); err != nil {
		watcher.Close()
		log.Printf("runtime: container %s: warning: could not watch %s: %v", containerID, claudeDir, err)
		return s, nil
	}

	s.watcher = watcher
	go watchAndRefreshCreds(containerID, claudeDir, stagingDir, watcher)
	return s, nil
}

// Close shuts down the fsnotify watcher and removes the staging directory.
func (s *CredentialStager) Close() {
	if s.watcher != nil {
		s.watcher.Close()
		s.watcher = nil
	}
	if s.StagingDir != "" {
		os.RemoveAll(s.StagingDir)
		s.StagingDir = ""
	}
}

// copyCredsToStaging copies .credentials.json, settings.json, and settings.local.json
// from claudeDir to stagingDir. Missing files are silently skipped.
func copyCredsToStaging(claudeDir, stagingDir string) error {
	for _, name := range []string{".credentials.json", "settings.json", "settings.local.json"} {
		src := filepath.Join(claudeDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // file doesn't exist or is unreadable; skip
		}
		if err := os.WriteFile(filepath.Join(stagingDir, name), data, 0644); err != nil {
			return fmt.Errorf("writing %s to staging dir: %w", name, err)
		}
	}
	return nil
}

// watchAndRefreshCreds listens on the watcher for Write or Create events on
// .credentials.json in claudeDir and re-copies the file to stagingDir in-place
// (os.WriteFile, same inode). Atomic host renames emit a Create event for
// the new file, which triggers the re-copy without inode churn in the container.
// Watcher errors are logged as warnings and do not terminate the goroutine.
func watchAndRefreshCreds(containerID, claudeDir, stagingDir string, watcher *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != ".credentials.json" {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			src := filepath.Join(claudeDir, ".credentials.json")
			data, err := os.ReadFile(src)
			if err != nil {
				log.Printf("runtime: container %s: warning: re-reading .credentials.json: %v", containerID, err)
				continue
			}
			dst := filepath.Join(stagingDir, ".credentials.json")
			if err := os.WriteFile(dst, data, 0644); err != nil {
				log.Printf("runtime: container %s: warning: re-staging .credentials.json: %v", containerID, err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("runtime: container %s: warning: fsnotify error: %v", containerID, err)
		}
	}
}
