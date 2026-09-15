package scan

import (
	_ "embed"

	"github.com/swordsmanluke/cloche/internal/domain"
)

//go:embed prompts/discover-domains.md
var discoverDomainsPrompt string

//go:embed prompts/extract.md
var extractPrompt string

//go:embed prompts/reconcile.md
var reconcilePrompt string

const collectSourcesScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
OUT="$TEMP/intent-scan-sources"
cloche intent collect-sources --project "$PROJECT_DIR" --out "$OUT"
cloche set intent_scan_sources_dir "$OUT"
`

const applyReconcileScript = `set -eu
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)
if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi
RECONCILE_FILE="$TEMP/reconcile.json"
if [ ! -f "$RECONCILE_FILE" ]; then
  echo "error: $RECONCILE_FILE not found — did the reconcile step write it?" >&2
  exit 1
fi
cloche intent apply-reconcile --project "$PROJECT_DIR" --reconcile-file "$RECONCILE_FILE"
`

// BuiltinWorkflow returns a freshly constructed intent-scan host workflow.
// Every call allocates new maps so callers (e.g. ResolveAgents) can mutate
// the result without affecting other resolutions.
func BuiltinWorkflow() *domain.Workflow {
	return &domain.Workflow{
		Name:     "intent-scan",
		Location: domain.LocationHost,
		Builtin:  true,
		Config: map[string]string{
			"_location_block":    "host",
			"host.agent_command": "claude",
		},
		EntryStep: "discover-domains",
		Steps: map[string]*domain.Step{
			"discover-domains": {
				Name: "discover-domains",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  discoverDomainsPrompt,
					"timeout": "10m",
				},
				Results: []string{"success", "fail"},
			},
			"collect-sources": {
				Name: "collect-sources",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": collectSourcesScript,
				},
				Results: []string{"success", "none", "fail"},
			},
			"extract": {
				Name: "extract",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  extractPrompt,
					"timeout": "20m",
				},
				Results: []string{"success", "fail"},
			},
			"reconcile": {
				Name: "reconcile",
				Type: domain.StepTypeAgent,
				Config: map[string]string{
					"prompt":  reconcilePrompt,
					"timeout": "20m",
				},
				Results: []string{"success", "fail"},
			},
			"apply-reconcile": {
				Name: "apply-reconcile",
				Type: domain.StepTypeScript,
				Config: map[string]string{
					"run": applyReconcileScript,
				},
				Results: []string{"success", "fail"},
			},
		},
		Wiring: []domain.Wire{
			{From: "discover-domains", Result: "success", To: "collect-sources"},
			{From: "discover-domains", Result: "fail", To: domain.StepAbort},
			{From: "collect-sources", Result: "success", To: "extract"},
			{From: "collect-sources", Result: "none", To: domain.StepDone},
			{From: "collect-sources", Result: "fail", To: domain.StepAbort},
			{From: "extract", Result: "success", To: "reconcile"},
			{From: "extract", Result: "fail", To: domain.StepAbort},
			{From: "reconcile", Result: "success", To: "apply-reconcile"},
			{From: "reconcile", Result: "fail", To: domain.StepAbort},
			{From: "apply-reconcile", Result: "success", To: domain.StepDone},
			{From: "apply-reconcile", Result: "fail", To: domain.StepAbort},
		},
	}
}
