package docker

import (
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// credentialFiles are the auth-relevant files staged into each container.
var credentialFiles = []string{".credentials.json", "settings.json", "settings.local.json"}

// CreateCredentialStagingDir creates an isolated staging directory and copies
// credential files from claudeDir into it. The caller must call
// os.RemoveAll(stagingDir) when the container stops.
func CreateCredentialStagingDir(claudeDir string) (string, error) {
	stagingDir, err := os.MkdirTemp("", "cloche-claude-")
	if err != nil {
		return "", err
	}
	copyCredentials(claudeDir, stagingDir)
	return stagingDir, nil
}

// copyCredentials copies credential files from srcDir to dstDir.
// Files absent in srcDir are silently skipped.
func copyCredentials(srcDir, dstDir string) {
	for _, name := range credentialFiles {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(dstDir, name), data, 0644)
	}
}

// StartCredentialWatcher watches claudeDir for Write/Create events on
// .credentials.json and overwrites the file in stagingDir in-place (same
// inode) so the bind-mounted container sees updated credentials without inode
// churn. Watcher errors are logged as warnings and do not crash the caller.
// The caller must call watcher.Close() when the container stops.
func StartCredentialWatcher(stagingDir, claudeDir, containerID string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := watcher.Add(claudeDir); err != nil {
		watcher.Close()
		return nil, err
	}
	go watchCredentials(watcher, stagingDir, containerID)
	return watcher, nil
}

func watchCredentials(watcher *fsnotify.Watcher, stagingDir, containerID string) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != ".credentials.json" {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			data, err := os.ReadFile(event.Name)
			if err != nil {
				log.Printf("credential watcher: container %s: reading %s: %v", containerID, event.Name, err)
				continue
			}
			dst := filepath.Join(stagingDir, ".credentials.json")
			if err := os.WriteFile(dst, data, 0644); err != nil {
				log.Printf("credential watcher: container %s: updating staging dir: %v", containerID, err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("credential watcher: container %s: %v", containerID, err)
		}
	}
}
