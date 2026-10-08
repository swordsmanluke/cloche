// Package stepinput renders the input summary recorded for non-agent steps
// (see domain.StepInput). Agent steps record their rendered prompt verbatim;
// script and poll steps have no single text to record, so this package
// builds one from what the step is actually given.
package stepinput

import (
	"sort"
	"strings"
)

// Script renders a script step's input: the command line, every Cloche-
// provided variable with its value, the names of all other environment
// variables, and the names of the KV keys available to `cloche get`.
//
// Only CLOCHE_* variables carry values. The rest of the environment is
// inherited from the daemon or container and routinely holds tokens, so it
// is recorded by name alone — enough to see what the script could read
// without copying secrets into run metadata. KV values are likewise left
// out; a key's presence is what tells you a `cloche get` would have hit.
func Script(command string, env []string, kvKeys []string) string {
	var cloche, others []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CLOCHE_") {
			cloche = append(cloche, kv)
		} else {
			others = append(others, name)
		}
	}
	sort.Strings(cloche)
	others = dedupSorted(others)
	keys := dedupSorted(append([]string(nil), kvKeys...))

	var b strings.Builder
	b.WriteString("command:\n")
	writeIndented(&b, command)
	b.WriteString("\nenvironment (CLOCHE_*):\n")
	if len(cloche) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, kv := range cloche {
		b.WriteString("  " + kv + "\n")
	}
	b.WriteString("\nenvironment (other, names only):\n")
	if len(others) == 0 {
		b.WriteString("  (none)\n")
	} else {
		b.WriteString("  " + strings.Join(others, " ") + "\n")
	}
	b.WriteString("\nkv keys:\n")
	if len(keys) == 0 {
		b.WriteString("  (none)\n")
	} else {
		b.WriteString("  " + strings.Join(keys, " ") + "\n")
	}
	return b.String()
}

// Poll renders a poll step's input: its interval and the condition it
// evaluates, both after any interpolation the executor applied.
func Poll(interval, condition string) string {
	var b strings.Builder
	b.WriteString("interval: " + interval + "\n")
	b.WriteString("condition:\n")
	writeIndented(&b, condition)
	return b.String()
}

func writeIndented(b *strings.Builder, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		b.WriteString("  (empty)\n")
		return
	}
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("  " + line + "\n")
	}
}

func dedupSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, v := range in {
		if i == 0 || v != in[i-1] {
			out = append(out, v)
		}
	}
	return out
}
