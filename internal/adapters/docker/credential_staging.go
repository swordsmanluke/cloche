package docker

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// CredentialRefresher watches a host-side credential directory and re-delivers
// .credentials.json to a container whenever the host file changes (e.g. after
// an OAuth token rotation via atomic rename). The delivery mechanism is
// injected via copyFn so that unit tests can run without Docker.
type CredentialRefresher struct {
	// HostDir is the host-side directory being watched.
	HostDir      string
	// ContainerDir is the container-side destination directory for credentials.
	ContainerDir string

	watcher     *fsnotify.Watcher
	containerID string
	copyFn      func(src, containerID, destPath string) error
}

// NewCredentialRefresher creates a watcher that uses docker-cp to re-deliver
// .credentials.json into containerID at containerDir whenever the file changes
// in hostDir. Returns nil (with a warning log) if the watcher cannot be set up.
func NewCredentialRefresher(containerID, hostDir, containerDir string) *CredentialRefresher {
	return newCredentialRefresher(containerID, hostDir, containerDir, dockerCpCredFile)
}

// NewCredentialRefresherWithCopy creates a watcher with a custom copy function.
// Intended for unit tests that need to verify refresh behavior without Docker.
func NewCredentialRefresherWithCopy(containerID, hostDir, containerDir string, copyFn func(src, containerID, destPath string) error) *CredentialRefresher {
	return newCredentialRefresher(containerID, hostDir, containerDir, copyFn)
}

// newCredentialRefresher is the internal constructor; copyFn is injectable for tests.
func newCredentialRefresher(containerID, hostDir, containerDir string, copyFn func(src, containerID, destPath string) error) *CredentialRefresher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("cred-refresh: container %s: warning: could not create watcher: %v", containerID, err)
		return nil
	}
	if err := w.Add(hostDir); err != nil {
		w.Close()
		log.Printf("cred-refresh: container %s: warning: could not watch %s: %v", containerID, hostDir, err)
		return nil
	}
	cr := &CredentialRefresher{
		HostDir:      hostDir,
		ContainerDir: containerDir,
		watcher:      w,
		containerID:  containerID,
		copyFn:       copyFn,
	}
	go cr.run()
	return cr
}

func (cr *CredentialRefresher) run() {
	for {
		select {
		case event, ok := <-cr.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != ".credentials.json" {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			src := filepath.Join(cr.HostDir, ".credentials.json")
			if _, err := os.Stat(src); err != nil {
				continue // file may not exist transiently during rotation
			}
			destPath := cr.ContainerDir + ".credentials.json"
			if err := cr.copyFn(src, cr.containerID, destPath); err != nil {
				log.Printf("cred-refresh: container %s: warning: re-copy failed: %v", cr.containerID, err)
			}
		case err, ok := <-cr.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("cred-refresh: container %s: warning: %v", cr.containerID, err)
		}
	}
}

// Close stops the watcher.
func (cr *CredentialRefresher) Close() {
	if cr.watcher != nil {
		if err := cr.watcher.Close(); err != nil {
			log.Printf("cred-refresh: container %s: close watcher: %v", cr.containerID, err)
		}
		cr.watcher = nil
	}
}

// dockerCpCredFile is the production copy function: docker cp src containerID:destPath.
func dockerCpCredFile(src, containerID, destPath string) error {
	out, err := exec.Command("docker", "cp", src, containerID+":"+destPath).CombinedOutput()
	if err != nil {
		log.Printf("cred-refresh: docker cp %s → %s:%s: %s: %v", src, containerID, destPath, string(out), err)
	}
	return err
}
