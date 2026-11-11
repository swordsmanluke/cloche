package dra

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionsFile is the daemon's record of each DRA's named Claude Code
// session, under the project's gitignored runtime directory .cloche/runs/.
const SessionsFile = "dra-sessions.json"

// Session is what the daemon remembers about one DRA between wake-ups.
type Session struct {
	// ID is the named Claude Code session (--session-id the first time,
	// --resume afterwards). Empty until the first wake-up.
	ID string `json:"session_id,omitempty"`
	// LastRunID is the run record of the latest wake-up, LastWakeAt when it
	// started, LastResult how it ended ("running", "succeeded", "failed")
	// and LastMode how the session was obtained ("created", "resumed" or
	// "fallback": resume failed and a new session was started).
	LastRunID  string    `json:"last_run_id,omitempty"`
	LastWakeAt time.Time `json:"last_wake_at,omitempty"`
	LastResult string    `json:"last_result,omitempty"`
	LastMode   string    `json:"last_mode,omitempty"`
}

// Session modes.
const (
	ModeCreated  = "created"
	ModeResumed  = "resumed"
	ModeFallback = "fallback"
)

var sessionMu sync.Mutex

// SessionsPath is the sessions file of a project.
func SessionsPath(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", "runs", SessionsFile)
}

// LoadSessions reads every DRA's session record, keyed by DRA name. A
// missing file is an empty map.
func LoadSessions(projectDir string) (map[string]Session, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return loadSessions(projectDir)
}

func loadSessions(projectDir string) (map[string]Session, error) {
	out := map[string]Session{}
	data, err := os.ReadFile(SessionsPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("reading %s: %w", SessionsPath(projectDir), err)
	}
	return out, nil
}

// UpdateSession applies mutate to the named DRA's record and saves it.
func UpdateSession(projectDir, name string, mutate func(*Session)) error {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	all, err := loadSessions(projectDir)
	if err != nil {
		return err
	}
	s := all[name]
	mutate(&s)
	all[name] = s
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	path := SessionsPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// NewSessionID returns a random UUID (version 4), the form Claude Code's
// --session-id requires.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
