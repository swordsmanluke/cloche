// Package builtin holds the registry of workflows constructed in Go and
// registered in the daemon, resolved by name after project .cloche file
// discovery — a project-defined workflow with the same name overrides the
// built-in. Name-keyed and generic so it can grow beyond intent-scan.
package builtin

import (
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/intent/scan"
)

// factories returns a fresh *domain.Workflow per call so callers can't
// mutate a shared instance across resolutions.
var factories = map[string]func() *domain.Workflow{
	"intent-scan": scan.BuiltinWorkflow,
}

// Lookup returns the built-in workflow with the given name, if any.
func Lookup(name string) (*domain.Workflow, bool) {
	factory, ok := factories[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}

// All returns a fresh instance of every registered built-in workflow, keyed
// by name.
func All() map[string]*domain.Workflow {
	all := make(map[string]*domain.Workflow, len(factories))
	for name, factory := range factories {
		all[name] = factory()
	}
	return all
}
