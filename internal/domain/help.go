package domain

import (
	"crypto/rand"
	"fmt"
	"time"
)

// ThreadState is the lifecycle state of a HelpThread.
type ThreadState string

const (
	ThreadStateAwaitingUser  ThreadState = "awaiting_user"
	ThreadStateAwaitingAgent ThreadState = "awaiting_agent"
	ThreadStateClosed        ThreadState = "closed"
	ThreadStateArchived      ThreadState = "archived"
)

// MessageAuthor identifies who wrote a HelpMessage.
type MessageAuthor string

const (
	MessageAuthorAgent MessageAuthor = "agent"
	MessageAuthorUser  MessageAuthor = "user"
)

// HelpThread is a conversation opened by an agent step to ask the user a
// question. Threads are addressed externally by <Channel>/<Name>, and
// internally by their stable ID.
type HelpThread struct {
	ID         string // thread-<random> (stable internal id)
	Channel    string // cloche-side channel name, e.g. project name ("cloche", "mazd")
	Name       string // human-addressable name, unique within Channel (<slug>-<seq>)
	TaskID     string
	AttemptID  string
	RunID      string
	StepName   string
	Title      string // short summary, shown in channel headers / thread list
	State      ThreadState
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt *time.Time // set when the owning task completes successfully
}

// Address returns the thread's external <channel>/<name> address.
func (t *HelpThread) Address() string {
	return t.Channel + "/" + t.Name
}

// HelpMessage is a single message within a HelpThread.
type HelpMessage struct {
	ID        string
	ThreadID  string
	Author    MessageAuthor
	Body      string
	Options   []string // optional suggested answers (agent messages only)
	AskKey    string   // idempotency key for replay (see Parking)
	CreatedAt time.Time
}

// threadIDAlphabet is the set of characters used for generated help IDs.
const threadIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func generateHelpID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	for i := range b {
		b[i] = threadIDAlphabet[int(b[i])%len(threadIDAlphabet)]
	}
	return prefix + "-" + string(b)
}

// GenerateHelpThreadID produces a unique thread ID of the form "thread-<random>".
func GenerateHelpThreadID() string {
	return generateHelpID("thread")
}

// GenerateHelpMessageID produces a unique message ID of the form "msg-<random>".
func GenerateHelpMessageID() string {
	return generateHelpID("msg")
}
