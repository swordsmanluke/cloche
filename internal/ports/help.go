package ports

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

// HelpChannel delivers agent questions to the user over one integration and
// feeds user replies back through the sink. Implementations must be safe to
// run for the daemon's lifetime.
type HelpChannel interface {
	Name() string // "cli", "slack", ...
	// Start runs the channel until ctx is done. Replies are pushed into sink.
	Start(ctx context.Context, sink ReplySink) error
	// Post delivers a new agent message on a thread. Called for every
	// configured channel; errors are logged, not fatal (other channels and
	// the CLI inbox still work).
	Post(ctx context.Context, thread domain.HelpThread, msg domain.HelpMessage) error
}

// ReplySink accepts user replies to a help thread from any HelpChannel.
type ReplySink interface {
	// Reply appends a user message to a thread. First reply to an
	// awaiting_user thread unblocks/unparks the asking run.
	Reply(ctx context.Context, threadID string, body string, via string) error
}
