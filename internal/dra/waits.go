package dra

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DRA waits. A DRA ends its wake-up and is woken when something it waits on is
// ready. It names a poll; it never supplies script text. A poll is either a
// script the project's owner wrote at .cloche/dras/<dra>/polls/<poll>.sh, or
// the built-in "questions" poll, which runs `cloche questions await <ids>`.
// Poll arguments reach the script as CLOCHE_WAIT_ARG_<KEY> environment
// variables, never through a shell.

const (
	// PollsDirName is the directory, under a DRA's directory, of its polls.
	PollsDirName = "polls"
	// PollScriptExt is the extension of a poll script.
	PollScriptExt = ".sh"
	// QuestionsPoll is the built-in poll that waits on question ids.
	QuestionsPoll = "questions"
	// QuestionsPollResult is the result name `cloche questions await` prints
	// once no question is open.
	QuestionsPollResult = "answered"
	// WaitArgEnvPrefix prefixes the environment variable of each poll argument.
	WaitArgEnvPrefix = "CLOCHE_WAIT_ARG_"

	// MaxPendingWaits is how many pending waits one DRA may hold.
	MaxPendingWaits = 20
	// MinWaitInterval is the shortest poll interval a wait may ask for.
	MinWaitInterval = time.Minute
	// DefaultWaitInterval is used when a wait names no interval.
	DefaultWaitInterval = 5 * time.Minute
	// DefaultWaitTimeout is used when a wait names no timeout.
	DefaultWaitTimeout = 72 * time.Hour

	maxWaitNote     = 500
	maxWaitArgs     = 16
	maxWaitArgBytes = 4096
)

// Outcomes of a wait, as named in its wait event.
const (
	WaitReady   = "ready"
	WaitFailed  = "failed"
	WaitTimeout = "timeout"
)

var (
	pollNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	argKeyRE   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	questionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// WaitRequest is what a DRA asks for: a named poll, its arguments, how often
// to run it and how long to keep waiting.
type WaitRequest struct {
	Poll     string            `json:"poll"`
	Args     map[string]string `json:"args,omitempty"`
	Interval time.Duration     `json:"interval"`
	Timeout  time.Duration     `json:"timeout"`
	Note     string            `json:"note"`
}

// Wait is one registered wait.
type Wait struct {
	ID string `json:"id"`
	WaitRequest
	DRA        string    `json:"dra,omitempty"`
	Project    string    `json:"project,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastPollAt time.Time `json:"last_poll_at,omitempty"`
	LastOutput string    `json:"last_output,omitempty"`
}

// PollSpec is one poll invocation: a program and its arguments, run directly
// (not through a shell), with Env added to the daemon's environment.
type PollSpec struct {
	DRA  string // the DRA the wait belongs to; names the recorded run dra-wait-<DRA>
	Argv []string
	Env  []string
	Dir  string
}

// PollsDir is where a DRA's poll scripts live.
func PollsDir(projectDir, name string) string {
	return filepath.Join(Root(projectDir), name, PollsDirName)
}

// ListPolls returns the names of the polls the project's owner wrote for the
// DRA (not including the built-in "questions" poll), sorted.
func ListPolls(projectDir, name string) []string {
	entries, err := os.ReadDir(PollsDir(projectDir, name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), PollScriptExt)
		if n == e.Name() || !pollNameRE.MatchString(n) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(PollsDir(projectDir, name), e.Name())); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// EnvKey is the environment variable name of poll argument key.
func EnvKey(key string) string { return WaitArgEnvPrefix + strings.ToUpper(key) }

// ArgEnv renders args as CLOCHE_WAIT_ARG_<KEY>=value environment entries,
// sorted by key. Values are passed verbatim.
func ArgEnv(args map[string]string) []string {
	var out []string
	for k, v := range args {
		out = append(out, EnvKey(k)+"="+v)
	}
	sort.Strings(out)
	return out
}

// ResolvePoll turns a wait's poll name and arguments into the invocation to
// run. It refuses an unknown name, listing the DRA's polls, and arguments
// that cannot be passed safely. The name never reaches a path unless it
// matches a poll the owner wrote, and nothing the DRA supplied is ever run
// through a shell.
func ResolvePoll(projectDir, name string, req WaitRequest) (PollSpec, error) {
	if req.Poll == QuestionsPoll {
		ids := splitList(req.Args["ids"])
		if len(ids) == 0 || len(req.Args) != 1 {
			return PollSpec{}, fmt.Errorf("the questions poll takes exactly one argument, ids (Q14,Q15)")
		}
		for _, id := range ids {
			if !questionRE.MatchString(id) {
				return PollSpec{}, fmt.Errorf("invalid question id %q", id)
			}
		}
		return PollSpec{DRA: name, Argv: append([]string{"cloche", "questions", "await"}, ids...), Dir: projectDir}, nil
	}
	known := ListPolls(projectDir, name)
	found := false
	for _, p := range known {
		found = found || p == req.Poll
	}
	if !found {
		have := "(none)"
		if len(known) > 0 {
			have = strings.Join(known, ", ")
		}
		return PollSpec{}, fmt.Errorf("unknown poll %s; this DRA has: %s", req.Poll, have)
	}
	if len(req.Args) > maxWaitArgs {
		return PollSpec{}, fmt.Errorf("too many poll arguments (limit %d)", maxWaitArgs)
	}
	for k, v := range req.Args {
		if !argKeyRE.MatchString(k) {
			return PollSpec{}, fmt.Errorf("invalid argument name %q (letters, digits and _; must start with a letter)", k)
		}
		if strings.ContainsRune(v, 0) || len(v) > maxWaitArgBytes {
			return PollSpec{}, fmt.Errorf("argument %s: value must be text under %d bytes", k, maxWaitArgBytes)
		}
	}
	script := filepath.Join(PollsDir(projectDir, name), req.Poll+PollScriptExt)
	return PollSpec{DRA: name, Argv: []string{script}, Env: ArgEnv(req.Args), Dir: projectDir}, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Normalize fills the defaults and checks the limits that need no project:
// note, interval and timeout.
func (r *WaitRequest) Normalize() error {
	if strings.TrimSpace(r.Note) == "" {
		return fmt.Errorf("a wait needs a note saying what it is for")
	}
	if len(r.Note) > maxWaitNote {
		return fmt.Errorf("note is too long (limit %d bytes)", maxWaitNote)
	}
	if r.Interval == 0 {
		r.Interval = DefaultWaitInterval
	}
	if r.Interval < MinWaitInterval {
		return fmt.Errorf("interval %s is under the %s minimum interval", r.Interval, MinWaitInterval)
	}
	if r.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if r.Timeout == 0 {
		r.Timeout = DefaultWaitTimeout
	}
	return nil
}

// Command builds the process for one poll invocation. The program and its
// arguments are passed as an argument vector, so nothing in them is parsed by
// a shell; Env is added to the daemon's environment.
func (s PollSpec) Command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), s.Env...)
	return cmd
}
