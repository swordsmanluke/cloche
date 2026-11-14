package web

import "context"

// JudgeLauncher starts the built-in judge for freshly asked questions. The
// ask route calls it once per ask, after the ask slot succeeded.
type JudgeLauncher interface {
	LaunchJudge(ctx context.Context, projectDir string, questionIDs []string) error
}

// WithJudgeLauncher sets the launcher the ask route uses to start the judge.
func WithJudgeLauncher(l JudgeLauncher) HandlerOption {
	return func(h *Handler) { h.judgeLauncher = l }
}
