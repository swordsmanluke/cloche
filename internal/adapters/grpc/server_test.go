package grpc_test

import (
	"context"
	"testing"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_ListRuns_Empty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(context.Background(), &pb.ListRunsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Runs)
}

func TestServer_GetStatus_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: "nonexistent"})
	assert.Error(t, err)
}
