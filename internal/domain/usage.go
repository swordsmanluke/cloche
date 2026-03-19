package domain

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
}

// StepResult is the return value of AgentAdapter.Execute, combining the
// result string with optional token usage information.
type StepResult struct {
	Result string
	Usage  *TokenUsage
}
