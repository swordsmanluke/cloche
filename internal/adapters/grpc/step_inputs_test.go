package grpc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/swordsmanluke/cloche/api/clochepb"
	server "github.com/swordsmanluke/cloche/internal/adapters/grpc"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGetStepInputs_ResolvesStepIDs verifies the RPC resolves every id form
// that names a step — attempt:workflow:step, task:attempt:step and the
// four-part task:attempt:workflow:step — and refuses ids that don't.
func TestGetStepInputs_ResolvesStepIDs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	run := domain.NewRun("a41k-develop", "develop")
	run.TaskID = "TASK-1"
	run.AttemptID = "a41k"
	run.ProjectDir = "/p"
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{ID: "a41k", TaskID: "TASK-1"}))
	require.NoError(t, store.SaveStepInput(ctx, &domain.StepInput{RunID: "a41k-develop", StepName: "implement", Kind: domain.StepInputPrompt, Content: "first prompt"}))
	require.NoError(t, store.SaveStepInput(ctx, &domain.StepInput{RunID: "a41k-develop", StepName: "implement", Kind: domain.StepInputPrompt, Content: "retry prompt"}))

	srv := server.NewClocheServer(store, nil)

	for _, id := range []string{"a41k:develop:implement", "TASK-1:a41k:implement", "TASK-1:a41k:develop:implement"} {
		resp, err := srv.GetStepInputs(ctx, &pb.GetStepInputsRequest{Id: id})
		require.NoError(t, err, id)
		assert.Equal(t, "a41k-develop", resp.RunId, id)
		assert.Equal(t, "implement", resp.StepName, id)
		require.Len(t, resp.Inputs, 2, id)
		assert.Equal(t, int32(1), resp.Inputs[0].Attempt)
		assert.Equal(t, "first prompt", resp.Inputs[0].Content)
		assert.Equal(t, "prompt", resp.Inputs[0].Kind)
		assert.Equal(t, int32(2), resp.Inputs[1].Attempt)
		assert.NotEmpty(t, resp.Inputs[1].CreatedAt)
	}

	// A step with nothing recorded resolves to an empty list, not an error.
	resp, err := srv.GetStepInputs(ctx, &pb.GetStepInputsRequest{Id: "a41k:develop:test"})
	require.NoError(t, err)
	assert.Empty(t, resp.Inputs)

	// Ids that stop at the run level are rejected with a hint.
	_, err = srv.GetStepInputs(ctx, &pb.GetStepInputsRequest{Id: "a41k:develop"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "does not name a step")

	_, err = srv.GetStepInputs(ctx, &pb.GetStepInputsRequest{Id: "nope:nope:nope"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestAgentSession_RecordsStepInputs verifies a StepInput message from the
// container is stored against the container's run, attempt by attempt.
func TestAgentSession_RecordsStepInputs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	run := domain.NewRun("run-inputs-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &fakeDockerRuntime{}
	pool := newFakePoolWithRuntime(rt)
	srv := server.NewClocheServerWithCaptures(store, store, rt.asContainerRuntime(), "")
	srv.SetContainerPool(pool)
	srv.RegisterContainerRun("ctr-inputs-1", "run-inputs-1")

	stream := newFakeAgentStream(ctx)
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_Ready{Ready: &pb.AgentReady{RunId: "ctr-inputs-1"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepInput{StepInput: &pb.StepInput{StepName: "implement", Kind: "prompt", Content: "Do the thing.\n"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepStarted{StepStarted: &pb.StepStarted{RequestId: "req-1", StepName: "implement"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepInput{StepInput: &pb.StepInput{StepName: "implement", Kind: "prompt", Content: "Try again.\n"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepInput{StepInput: &pb.StepInput{StepName: "test", Kind: "script", Content: "command:\n  go test\n"}}})
	stream.close()
	require.NoError(t, srv.AgentSession(stream))

	inputs, err := store.ListStepInputs(ctx, "run-inputs-1", "implement")
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	assert.Equal(t, "Do the thing.\n", inputs[0].Content)
	assert.Equal(t, domain.StepInputPrompt, inputs[0].Kind)
	assert.Equal(t, 2, inputs[1].Attempt)

	inputs, _ = store.ListStepInputs(ctx, "run-inputs-1", "test")
	require.Len(t, inputs, 1)
	assert.Equal(t, domain.StepInputScript, inputs[0].Kind)
}
