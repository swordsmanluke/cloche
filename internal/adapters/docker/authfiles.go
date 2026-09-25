package docker

import (
	"encoding/json"
	"sort"
)

// autonomousSettingsKeys lists the ~/.claude/settings.json keys an autonomous
// (non-interactive) container receives. Everything else in the host's file —
// hooks, model, effort and thinking preferences, permissions, plugins, output
// style, marketplaces — is host preference, not credentials: hook scripts
// point at host paths that do not exist in the container (they run and fail
// on every session start), and the rest silently changes how the agent
// behaves from one host to the next. An agent inside a workflow must take its
// instructions from the step prompt alone. Interactive consoles still get the
// whole file, because there the host user is the one driving.
var autonomousSettingsKeys = []string{
	"env",
	"apiKeyHelper",
	"forceLoginMethod",
	"forceLoginOrgUUID",
	"awsAuthRefresh",
	"awsCredentialExport",
	"otelHeadersHelper",
}

// filterAgentSettings reduces a settings.json to autonomousSettingsKeys and
// returns the sorted names of the keys kept. Input that is not a JSON object
// yields "{}" (and kept == nil): a file Claude Code itself could not parse
// carries nothing worth forwarding, and forwarding it unread would defeat
// the point of filtering.
func filterAgentSettings(data []byte) ([]byte, []string) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return []byte("{}\n"), nil
	}
	out := map[string]json.RawMessage{}
	var kept []string
	for _, k := range autonomousSettingsKeys {
		if v, ok := all[k]; ok {
			out[k] = v
			kept = append(kept, k)
		}
	}
	sort.Strings(kept)
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return []byte("{}\n"), nil
	}
	return append(b, '\n'), kept
}
