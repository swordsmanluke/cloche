package intent

import (
	"fmt"
	"strings"
)

// blockHeader is the fixed preamble of the injected requirements block: the
// section title plus the flag-don't-ignore instruction from the design.
const blockHeader = `## Standing project requirements

These are established constraints and decisions for this project. Follow them unless
the task explicitly overrides one. Requirement IDs like [req-a3f8] are for reference;
one you believe is wrong or outdated should be flagged in your output, not silently
ignored.
`

// FormatBlock renders the requirements block injected into an agent step's
// prompt: the standing header, one bullet per selected requirement, and — if
// the token budget dropped anything — a truncation marker naming how many
// more requirements exist. An empty selection (no active/in-scope
// requirements, or no intent dir at all) renders as "", so callers never
// inject an empty section.
func FormatBlock(result Result) string {
	if len(result.Selected) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(blockHeader)
	b.WriteString("\n")
	for _, s := range result.Selected {
		b.WriteString(formatRequirementLine(s.Requirement))
		b.WriteString("\n")
	}
	if result.Omitted > 0 {
		fmt.Fprintf(&b, "(%d more requirements omitted; run cloche intent list)\n", result.Omitted)
	}
	return b.String()
}

// formatRequirementLine renders one bullet: "- [id] (domain, ...) statement"
// for a domain-scoped requirement, or "- [id] statement" for a project-level
// one. The body's markdown (statement plus optional rationale) is collapsed
// to a single line so the block stays compact regardless of how the source
// file wraps it.
func formatRequirementLine(req *Requirement) string {
	var b strings.Builder
	b.WriteString("- [")
	b.WriteString(req.ID)
	b.WriteString("] ")
	if req.Scope.Level == ScopeLevelDomain && len(req.Scope.Domains) > 0 {
		b.WriteString("(")
		b.WriteString(strings.Join(req.Scope.Domains, ", "))
		b.WriteString(") ")
	}
	b.WriteString(collapseWhitespace(req.Body))
	return b.String()
}

// collapseWhitespace joins a (possibly multi-line, markdown-formatted)
// string's fields with single spaces.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
