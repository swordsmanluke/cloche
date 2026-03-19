package domain

// TokenUsage holds token consumption for a single agent step execution.
type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// UsageSummary holds aggregated token usage with burn rate metrics.
type UsageSummary struct {
	TotalInputTokens    int64
	TotalOutputTokens   int64
	InputTokensPerHour  float64
	OutputTokensPerHour float64
}

// StepResult is the return value of AgentAdapter.Execute, combining the
// result string with optional token usage information.
type StepResult struct {
	Result string
	Usage  *TokenUsage
}
