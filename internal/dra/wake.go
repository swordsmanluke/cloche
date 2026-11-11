package dra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// A wake-up is an ordinary run: a generated host workflow that dispatches
// container workflows in the DRA image, each with one prompt step.
//
//	resume --success--> done          (only when a session id is stored)
//	resume --fail-----> fresh
//	fresh  --success--> done
//
// "fresh" starts a new named session. It runs first when the DRA has no
// session yet, and as the fallback when resuming fails (the session is gone,
// compacted away, or the volume was lost). The session is disposable; the
// notes are the memory, so both prompts say to read notes.md first and
// update it before stopping.

// Wake-up request protocol over the RunWorkflow RPC (the proto has no
// dedicated message): WorkflowName is WakeRequest, Env carries the DRA's name
// and the message.
const (
	WakeRequest    = "dra-wake"
	WakeEnvDRA     = "dra"
	WakeEnvMessage = "message"
)

const (
	// WakeWorkflowPrefix prefixes the host workflow (and run) name of a
	// wake-up: "dra-wake-<name>".
	WakeWorkflowPrefix = "dra-wake-"
	// SessionHome is where the Claude Code session files live in the
	// container; the per-DRA volume is mounted here.
	SessionHome = "/home/agent/.claude"
	// MessageFile is the file, in the run's directory, holding the wake-up
	// message. It is a file, not prompt text, so a message containing `{{`
	// is never read as a template.
	MessageFile = "wake-message.md"

	wakeContainerID = "dra"
	wakeTimeout     = "60m"
)

// WorkflowName is the host workflow / run name of a DRA's wake-ups.
func WorkflowName(name string) string { return WakeWorkflowPrefix + name }

// VolumeName is the Docker volume holding the DRA's session files. It is
// per DRA and per project, so sessions survive between runs.
func VolumeName(projectDir, name string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(projectDir)))
	return "cloche-dra-" + hex.EncodeToString(sum[:4]) + "-" + name
}

// NotesRel is the project-relative path of the DRA's notes file.
func NotesRel(name string) string {
	return filepath.ToSlash(filepath.Join(".cloche", DirName, name, NotesFile))
}

// WakeSpec describes one wake-up.
type WakeSpec struct {
	Name string
	// RunID is the wake-up's run id; the message file lives in its run dir.
	RunID string
	// ResumeID is the stored session to resume ("" when there is none).
	ResumeID string
	// NewID is the session id a fresh start will use.
	NewID string
}

// MessagePath is the container-side path of the wake-up message.
func (w WakeSpec) MessagePath() string {
	return ".cloche/runs/" + w.RunID + "/" + MessageFile
}

// Prompt is the prompt of a wake-up step. fresh means a new session, which
// cannot remember anything, so it is told to rebuild its context from the
// charter and the notes.
func (w WakeSpec) Prompt(fresh bool) string {
	charter := filepath.ToSlash(filepath.Join(".cloche", DirName, w.Name, CharterFile))
	var b strings.Builder
	fmt.Fprintf(&b, "You are the Domain Responsible Agent for the %q domain. You have just been woken up.\n\n", w.Name)
	fmt.Fprintf(&b, "What happened is in %s. Read it.\n\n", w.MessagePath())
	fmt.Fprintf(&b, "Read %s (your charter) and %s (your notes, your memory between wake-ups) first.\n", charter, NotesRel(w.Name))
	if fresh {
		b.WriteString("This is a new session: you remember nothing from before. The charter and the notes are all you have, so rely on them.\n")
	}
	b.WriteString("Then do what the charter says is yours to do about what happened.\n")
	fmt.Fprintf(&b, "Before you stop, update %s with whatever the next wake-up needs to know: this session may be lost.\n", NotesRel(w.Name))
	b.WriteString("You manage work; you never change code or stop or delete runs.\n")
	return b.String()
}

func (w WakeSpec) containerWorkflow(name string, fresh bool) *domain.Workflow {
	agentArgs := "-p --output-format stream-json --verbose --dangerously-skip-permissions --model sonnet"
	if fresh {
		agentArgs += " --session-id " + w.NewID
	} else {
		agentArgs += " --resume " + w.ResumeID
	}
	return &domain.Workflow{
		Name:     name,
		Location: domain.LocationContainer,
		Builtin:  true,
		Config: map[string]string{
			"_location_block":   "container",
			"container.id":      wakeContainerID,
			"container.profile": "dra",
			"container.dra":     w.Name,
			"container.notes":   NotesRel(w.Name),
		},
		EntryStep: "wake",
		Steps: map[string]*domain.Step{
			"wake": {
				Name: "wake",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":     w.Prompt(fresh),
					"agent_args": agentArgs,
					"timeout":    wakeTimeout,
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "wake", Result: "success", To: domain.StepDone},
			{From: "wake", Result: "fail", To: domain.StepAbort},
		},
	}
}

// Workflows returns the host workflow of a wake-up and the container
// workflows it calls, to be made resolvable by name.
func (w WakeSpec) Workflows() (hostWF *domain.Workflow, containers map[string]*domain.Workflow) {
	resumeName := WakeWorkflowPrefix + w.Name + "-resume"
	freshName := WakeWorkflowPrefix + w.Name + "-fresh"
	containers = map[string]*domain.Workflow{
		freshName: w.containerWorkflow(freshName, true),
	}
	step := func(name, target string) *domain.Step {
		return &domain.Step{
			Name:    name,
			Type:    domain.StepTypeWorkflow,
			Config:  map[string]string{"workflow_name": target},
			Results: []string{"success", "fail"},
		}
	}
	hostWF = &domain.Workflow{
		Name:     WorkflowName(w.Name),
		Location: domain.LocationHost,
		Builtin:  true,
		Config:   map[string]string{"_location_block": "host"},
		Steps:    map[string]*domain.Step{"fresh": step("fresh", freshName)},
		Wiring: []domain.Wire{
			{From: "fresh", Result: "success", To: domain.StepDone},
			{From: "fresh", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "fresh",
	}
	if w.ResumeID != "" {
		containers[resumeName] = w.containerWorkflow(resumeName, false)
		hostWF.Steps["resume"] = step("resume", resumeName)
		hostWF.EntryStep = "resume"
		hostWF.Wiring = append(hostWF.Wiring,
			domain.Wire{From: "resume", Result: "success", To: domain.StepDone},
			domain.Wire{From: "resume", Result: "fail", To: "fresh"},
		)
	}
	return hostWF, containers
}

// WriteMessage writes the wake-up message into the run's directory (which
// the runtime mounts into the container).
func WriteMessage(projectDir, runID, message string) error {
	dir := filepath.Join(projectDir, ".cloche", "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MessageFile), []byte(message+"\n"), 0o644)
}

// Find returns the named DRA of a project, or an error if it is missing or
// invalid.
func Find(projectDir, name string) (DRA, error) {
	ds, err := List(projectDir)
	if err != nil {
		return DRA{}, err
	}
	for _, d := range ds {
		if d.Name == name {
			if !d.Valid() {
				return d, fmt.Errorf("DRA %q is invalid: %s", name, strings.Join(d.Problems, "; "))
			}
			return d, nil
		}
	}
	return DRA{}, fmt.Errorf("no DRA %q in %s", name, Root(projectDir))
}

// Outcome works out, from the host run's step records, the session to store
// and how it was obtained. "fresh" having run means a new session was
// started under spec.NewID (first wake-up, or the fallback after "resume"
// failed); "resume" succeeding means the stored session continues.
func (w WakeSpec) Outcome(steps []*domain.StepExecution) (sessionID, mode string) {
	var resumed, resumeFailed, fresh bool
	for _, s := range steps {
		switch {
		case s.Skipped:
		case s.StepName == "resume":
			resumed = true
			resumeFailed = s.Result != "success"
		case s.StepName == "fresh":
			fresh = true
		}
	}
	switch {
	case fresh && resumed && resumeFailed:
		return w.NewID, ModeFallback
	case fresh:
		return w.NewID, ModeCreated
	case resumed && !resumeFailed:
		return w.ResumeID, ModeResumed
	}
	return w.ResumeID, ""
}
