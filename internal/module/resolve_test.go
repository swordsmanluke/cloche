package module_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/module"
)

func wfn(name, mod string) *domain.Workflow {
	return &domain.Workflow{Name: name, Module: mod}
}

func bind(slot, target string, force bool) domain.Bind {
	return domain.Bind{Slot: slot, Target: target, Force: force}
}

func fr(name string, scopes ...module.Scope) module.Frame {
	return module.Frame{Workflow: name, Scopes: scopes}
}

func sc(kind module.ScopeKind, mod string, bs ...domain.Bind) module.Scope {
	return module.Scope{Kind: kind, Module: mod, Binds: bs}
}

func newResolver() *module.Resolver {
	rubyReview := wfn("code-review", "ruby-review")
	return &module.Resolver{
		Registry: &module.Registry{
			Modules: map[string]*module.Module{
				"ruby-review": {
					Manifest:  module.Manifest{Name: "ruby-review", Exports: []string{"code-review", "review"}},
					Workflows: map[string]*domain.Workflow{"code-review": rubyReview, "review": wfn("review", "ruby-review"), "rubocop": wfn("rubocop", "ruby-review")},
				},
				"clash": {Manifest: module.Manifest{Name: "clash", Exports: []string{"code-review"}}, Workflows: map[string]*domain.Workflow{"code-review": wfn("code-review", "clash")}},
			},
			Root: []domain.Bind{bind("code-review", "ruby-review", false)},
		},
		Project: map[string]*domain.Workflow{
			"code-review": wfn("code-review", ""), "quick-review": wfn("quick-review", ""),
			"clash": wfn("clash", ""), "lint": wfn("lint", ""),
		},
		Builtin: func(n string) (*domain.Workflow, bool) {
			if n == "intent-scan" {
				return wfn(n, ""), true
			}
			return nil, false
		},
	}
}

func TestResolveSlot_Order(t *testing.T) {
	r := newResolver()

	got, err := r.ResolveSlot(nil, "code-review")
	require.NoError(t, err)
	assert.Equal(t, "ruby-review", got.Workflow.Module, "root bind")

	got, err = r.ResolveSlot(nil, "quick-review")
	require.NoError(t, err)
	assert.Equal(t, "project workflow", got.Source)

	got, err = r.ResolveSlot(nil, "intent-scan")
	require.NoError(t, err)
	assert.Equal(t, "built-in", got.Source)

	_, err = r.ResolveSlot(nil, "nope")
	assert.Error(t, err)
}

func TestResolveSlot_InnermostWins(t *testing.T) {
	r := newResolver()
	stack := module.CallStack{
		fr("outer", sc(module.ScopeFile, "", bind("code-review", "ruby-review:review", false))),
		fr("inner",
			sc(module.ScopeStep, "", bind("code-review", "quick-review", false)),
			sc(module.ScopeWorkflow, "", bind("code-review", "ruby-review", false))),
	}
	got, err := r.ResolveSlot(stack, "code-review")
	require.NoError(t, err)
	assert.Equal(t, "quick-review", got.Workflow.Name)

	// Dynamic: a caller's bind reaches a deeper call with no binds of its own.
	stack = module.CallStack{
		fr("outer", sc(module.ScopeFile, "", bind("code-review", "ruby-review:review", false))),
		fr("inner"),
	}
	got, err = r.ResolveSlot(stack, "code-review")
	require.NoError(t, err)
	assert.Equal(t, "review", got.Workflow.Name)
}

func TestResolveSlot_ForceBeatsInner_OuterForceWins(t *testing.T) {
	r := newResolver()
	stack := module.CallStack{
		fr("a", sc(module.ScopeFile, "", bind("lint", "lint", true))),
		fr("b", sc(module.ScopeWorkflow, "ruby-review", bind("lint", "rubocop", false))),
	}
	got, err := r.ResolveSlot(stack, "lint")
	require.NoError(t, err)
	assert.Equal(t, "", got.Workflow.Module, "bind! beats the module's own bind")

	stack = append(stack, fr("c", sc(module.ScopeStep, "", bind("lint", "rubocop", true))))
	stack[2].Scopes[0].Module = "ruby-review"
	got, err = r.ResolveSlot(stack, "lint")
	require.NoError(t, err)
	assert.Equal(t, "lint", got.Workflow.Name, "outer bind! wins over inner bind!")
	assert.Equal(t, "", got.Workflow.Module)
}

func TestResolveSlot_ModuleLocalBind(t *testing.T) {
	r := newResolver()
	stack := module.CallStack{fr("code-review", sc(module.ScopeWorkflow, "ruby-review", bind("lint", "rubocop", false)))}
	got, err := r.ResolveSlot(stack, "lint")
	require.NoError(t, err)
	assert.Equal(t, "ruby-review", got.Workflow.Module)
}

func TestResolveSlot_Ambiguous(t *testing.T) {
	r := newResolver()
	stack := module.CallStack{fr("a", sc(module.ScopeFile, "", bind("code-review", "clash", false)))}
	_, err := r.ResolveSlot(stack, "code-review")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")

	stack = module.CallStack{fr("a", sc(module.ScopeFile, "", bind("code-review", "clash:code-review", false)))}
	got, err := r.ResolveSlot(stack, "code-review")
	require.NoError(t, err)
	assert.Equal(t, "clash", got.Workflow.Module)
}

func TestResolveSlot_BadTarget(t *testing.T) {
	r := newResolver()
	stack := module.CallStack{fr("a", sc(module.ScopeFile, "", bind("code-review", "ghost", false)))}
	_, err := r.ResolveSlot(stack, "code-review")
	assert.Error(t, err)
	stack = module.CallStack{fr("a", sc(module.ScopeFile, "", bind("code-review", "ruby-review:private", false)))}
	_, err = r.ResolveSlot(stack, "code-review")
	assert.Error(t, err)
}

func TestStackRoundTripAndContext(t *testing.T) {
	stack := module.CallStack{fr("a", sc(module.ScopeFile, "m", bind("x", "y", true)))}
	enc, err := module.EncodeStack(stack)
	require.NoError(t, err)
	dec, err := module.DecodeStack(enc)
	require.NoError(t, err)
	assert.Equal(t, stack, dec)

	ctx := module.WithStack(context.Background(), stack)
	assert.Equal(t, stack, module.StackFromContext(ctx))
	pushed := stack.Push(fr("b"))
	assert.Len(t, stack, 1, "Push does not mutate")
	assert.Len(t, pushed, 2)
}

func TestFrameFor(t *testing.T) {
	fb := bind("a", "b", false)
	reg := &module.Registry{FileBinds: map[string][]domain.Bind{"f.cloche": {fb}}}
	wf := &domain.Workflow{Name: "w", SourceFile: "f.cloche", Binds: []domain.Bind{bind("c", "d", false)}}
	step := &domain.Step{Name: "s", Binds: []domain.Bind{bind("e", "f", false)}}
	f := reg.FrameFor(wf, step)
	require.Len(t, f.Scopes, 3)
	assert.Equal(t, module.ScopeStep, f.Scopes[0].Kind)
	assert.Equal(t, module.ScopeWorkflow, f.Scopes[1].Kind)
	assert.Equal(t, module.ScopeFile, f.Scopes[2].Kind)

	var nilReg *module.Registry
	assert.Len(t, nilReg.FrameFor(wf, step).Scopes, 2)
}
