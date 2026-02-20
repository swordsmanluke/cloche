package grpc

import (
	"context"
	"fmt"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/ports"
)

type ClocheServer struct {
	pb.UnimplementedClocheServiceServer
	store     ports.RunStore
	container ports.ContainerRuntime
}

func NewClocheServer(store ports.RunStore, container ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{store: store, container: container}
}

func (s *ClocheServer) ListRuns(ctx context.Context, req *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	runs, err := s.store.ListRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	resp := &pb.ListRunsResponse{}
	for _, run := range runs {
		resp.Runs = append(resp.Runs, &pb.RunSummary{
			RunId:        run.ID,
			WorkflowName: run.WorkflowName,
			State:        string(run.State),
			StartedAt:    run.StartedAt.String(),
		})
	}
	return resp, nil
}

func (s *ClocheServer) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}

	resp := &pb.GetStatusResponse{
		RunId:        run.ID,
		WorkflowName: run.WorkflowName,
		State:        string(run.State),
		CurrentStep:  run.CurrentStep,
	}

	for _, exec := range run.StepExecutions {
		resp.StepExecutions = append(resp.StepExecutions, &pb.StepExecutionStatus{
			StepName:    exec.StepName,
			Result:      exec.Result,
			StartedAt:   exec.StartedAt.String(),
			CompletedAt: exec.CompletedAt.String(),
		})
	}

	return resp, nil
}

func (s *ClocheServer) StopRun(ctx context.Context, req *pb.StopRunRequest) (*pb.StopRunResponse, error) {
	// Will be implemented with container runtime integration
	return &pb.StopRunResponse{}, nil
}
