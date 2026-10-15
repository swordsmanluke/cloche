package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// ScopeKind names where a group of binds was written.
type ScopeKind string

const (
	ScopeStep     ScopeKind = "step"
	ScopeWorkflow ScopeKind = "workflow"
	ScopeFile     ScopeKind = "file"
	ScopeRoot     ScopeKind = "project"
)

// Scope is the binds written at one position.
type Scope struct {
	Kind ScopeKind
	// Module is the module the binds were written in; "" for project files.
	// A bare-name target written inside a module may name one of that
	// module's own workflows.
	Module string
	Binds  []domain.Bind
}

// Frame is the bind scopes contributed by one call level: the calling step,
// the workflow containing it, and that workflow's file (innermost first).
type Frame struct {
	Workflow string
	Step     string
	Scopes   []Scope
}

// CallStack lists frames outermost first; the last frame is the call site
// being resolved. It is what gets propagated to child runs.
type CallStack []Frame

// Push returns a new stack with f appended, leaving s untouched.
func (s CallStack) Push(f Frame) CallStack {
	out := make(CallStack, len(s)+1)
	copy(out, s)
	out[len(s)] = f
	return out
}

// EncodeStack serializes a call stack for hand-off to a container run.
func EncodeStack(s CallStack) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}

// DecodeStack is the inverse of EncodeStack. Empty input is an empty stack.
func DecodeStack(raw string) (CallStack, error) {
	if raw == "" {
		return nil, nil
	}
	var s CallStack
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("decode bind scope: %w", err)
	}
	return s, nil
}

// StackEnv is the environment variable carrying an encoded call stack into a
// container run.
const StackEnv = "CLOCHE_BIND_SCOPE"

type stackKey struct{}

// WithStack attaches the call stack to ctx so child runs inherit it.
func WithStack(ctx context.Context, s CallStack) context.Context {
	return context.WithValue(ctx, stackKey{}, s)
}

// StackFromContext returns the call stack carried by ctx, if any.
func StackFromContext(ctx context.Context) CallStack {
	s, _ := ctx.Value(stackKey{}).(CallStack)
	return s
}

// FrameFor builds the frame for step (which may be nil) of wf, reading
// file-level binds from the module or project registry.
func (r *Registry) FrameFor(wf *domain.Workflow, step *domain.Step) Frame {
	f := Frame{Workflow: wf.Name}
	var fileBinds []domain.Bind
	if r != nil {
		if wf.Module != "" {
			if m, ok := r.Modules[wf.Module]; ok {
				fileBinds = m.FileBinds[wf.SourceFile]
			}
		} else {
			fileBinds = r.FileBinds[wf.SourceFile]
		}
	}
	if step != nil {
		f.Step = step.Name
		if len(step.Binds) > 0 {
			f.Scopes = append(f.Scopes, Scope{Kind: ScopeStep, Module: wf.Module, Binds: step.Binds})
		}
	}
	if len(wf.Binds) > 0 {
		f.Scopes = append(f.Scopes, Scope{Kind: ScopeWorkflow, Module: wf.Module, Binds: wf.Binds})
	}
	if len(fileBinds) > 0 {
		f.Scopes = append(f.Scopes, Scope{Kind: ScopeFile, Module: wf.Module, Binds: fileBinds})
	}
	return f
}

// ErrSlotNotFound is wrapped by ResolveSlot's error when nothing — no bind,
// project workflow or built-in — provides the slot.
var ErrSlotNotFound = errors.New("slot not found")

// Resolved is the outcome of ResolveSlot.
type Resolved struct {
	Workflow *domain.Workflow
	// Source describes where the choice came from, e.g. "bind in file
	// security.cloche", "project workflow" or "built-in".
	Source string
}

// Resolver resolves slots against a registry, the project's workflows, and
// the built-ins.
type Resolver struct {
	Registry *Registry
	Project  map[string]*domain.Workflow
	Builtin  func(name string) (*domain.Workflow, bool)
}

// ResolveSlot picks the workflow for slot at the call site at the top of
// stack. Order: step → workflow → file → caller frames … → project.cloche
// root → project workflow → built-in. The innermost bind wins, except that a
// bind! overrides every bind beneath it, and among competing bind!s the
// outermost wins.
func (rs *Resolver) ResolveSlot(stack CallStack, slot string) (*Resolved, error) {
	var chosen *domain.Bind
	var chosenScope Scope
	var chosenFrame string
	var forced bool

	consider := func(sc Scope, frame string) {
		// Walk each scope back to front so the last duplicate wins.
		for i := len(sc.Binds) - 1; i >= 0; i-- {
			b := sc.Binds[i]
			if b.Slot != slot {
				continue
			}
			switch {
			case b.Force:
				// Visited innermost first, so a later bind! is further out
				// and replaces an earlier one: the outermost bind! wins.
				chosen, chosenScope, chosenFrame, forced = &b, sc, frame, true
			case !forced && chosen == nil:
				// The first normal bind seen is the innermost.
				chosen, chosenScope, chosenFrame = &b, sc, frame
			}
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		fr := stack[i]
		label := fr.Workflow
		if fr.Step != "" {
			label += "/" + fr.Step
		}
		for _, sc := range fr.Scopes {
			consider(sc, label)
		}
	}
	if rs.Registry != nil && len(rs.Registry.Root) > 0 {
		consider(Scope{Kind: ScopeRoot, Binds: rs.Registry.Root}, "project.cloche")
	}

	if chosen != nil {
		wf, err := rs.resolveTarget(*chosen, chosenScope)
		if err != nil {
			return nil, fmt.Errorf("slot %q: bind in %s scope (%s): %w", slot, chosenScope.Kind, chosenFrame, err)
		}
		return &Resolved{Workflow: wf, Source: fmt.Sprintf("bind in %s scope (%s)", chosenScope.Kind, chosenFrame)}, nil
	}
	if wf, ok := rs.Project[slot]; ok {
		return &Resolved{Workflow: wf, Source: "project workflow"}, nil
	}
	if rs.Builtin != nil {
		if wf, ok := rs.Builtin(slot); ok {
			return &Resolved{Workflow: wf, Source: "built-in"}, nil
		}
	}
	return nil, fmt.Errorf("workflow %q not found: no bind, project workflow, or built-in: %w", slot, ErrSlotNotFound)
}

// resolveTarget resolves a bind's right-hand side: `module:workflow`, a bare
// module name (exporting a workflow named after the slot), or a workflow name.
func (rs *Resolver) resolveTarget(b domain.Bind, sc Scope) (*domain.Workflow, error) {
	reg := rs.Registry
	if reg == nil {
		reg = &Registry{}
	}
	if mod, name, ok := splitTarget(b.Target); ok {
		return reg.Export(mod, name)
	}

	_, isModule := reg.Modules[b.Target]
	var local *domain.Workflow
	if sc.Module != "" {
		if m, ok := reg.Modules[sc.Module]; ok {
			local = m.Workflows[b.Target]
		}
	}
	if local == nil {
		local = rs.Project[b.Target]
	}
	if isModule && local != nil {
		return nil, fmt.Errorf("ambiguous target %q: both an installed module and a workflow; use %q to pick the module's export", b.Target, b.Target+":"+b.Slot)
	}
	if isModule {
		return reg.Export(b.Target, b.Slot)
	}
	if local != nil {
		return local, nil
	}
	return nil, fmt.Errorf("target %q is neither an installed module nor a workflow", b.Target)
}

func splitTarget(t string) (mod, name string, ok bool) {
	for i := 0; i < len(t); i++ {
		if t[i] == ':' {
			return t[:i], t[i+1:], true
		}
	}
	return "", "", false
}
