package domain

// UnattributedAgent labels a usage record whose agent could not be
// determined from either the executing adapter or the step's config,
// distinguishing it from a record that was simply never populated.
const UnattributedAgent = "unattributed"

// TokenUsage holds token consumption for a single agent step execution.
type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	AgentName    string // "claude", "codex", etc.
}

// UsageSummary holds aggregated token usage with burn rate metrics.
type UsageSummary struct {
	AgentName     string
	InputTokens   int64
	OutputTokens  int64
	TotalTokens   int64
	WindowSeconds int64   // time window these stats cover
	BurnRate      float64 // total tokens per hour
	// InFlight is true when this summary includes tokens streamed from a
	// step that is still running, i.e. not yet superseded by that step's
	// final (persisted) usage. Display surfaces show "~" when set, since the
	// figure is a live estimate rather than a settled total.
	InFlight bool
}

// StepResult is the return value of AgentAdapter.Execute, combining the
// result string with optional token usage information.
type StepResult struct {
	Result  string
	Usage   *TokenUsage
	Skipped bool // true when the step's skip script decided to bypass execution
}
