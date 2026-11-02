package ports

import (
	"context"
	"log"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// AllocateAttemptID returns a new attempt ID from the store's counter. If the
// store is absent or allocation fails it returns a random ID so the run can
// proceed: a collision is then possible, but SaveAttempt rejects it rather than
// overwriting another task's attempt.
func AllocateAttemptID(ctx context.Context, store RunStore) string {
	if store == nil {
		return domain.GenerateAttemptID()
	}
	id, err := store.NextAttemptID(ctx)
	if err != nil {
		log.Printf("attempt ID allocation failed, using a random ID: %v", err)
		return domain.GenerateAttemptID()
	}
	return id
}
