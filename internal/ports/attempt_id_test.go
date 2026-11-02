package ports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swordsmanluke/cloche/internal/ports"
)

type allocatingStore struct {
	ports.RunStore
	id  string
	err error
}

func (s allocatingStore) NextAttemptID(context.Context) (string, error) { return s.id, s.err }

func TestAllocateAttemptID_UsesStoreCounter(t *testing.T) {
	assert.Equal(t, "zz01", ports.AllocateAttemptID(context.Background(), allocatingStore{id: "zz01"}))
}

func TestAllocateAttemptID_FallsBackWhenAllocationFails(t *testing.T) {
	id := ports.AllocateAttemptID(context.Background(), allocatingStore{err: errors.New("db locked")})
	assert.Len(t, id, 4)
}

func TestAllocateAttemptID_FallsBackWithoutStore(t *testing.T) {
	assert.Len(t, ports.AllocateAttemptID(context.Background(), nil), 4)
}
