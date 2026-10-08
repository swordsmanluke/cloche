package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"google.golang.org/grpc"
)

type stepInputMockClient struct {
	pb.ClocheServiceClient
	gotID string
	resp  *pb.GetStepInputsResponse
}

func (m *stepInputMockClient) GetStepInputs(_ context.Context, req *pb.GetStepInputsRequest, _ ...grpc.CallOption) (*pb.GetStepInputsResponse, error) {
	m.gotID = req.Id
	return m.resp, nil
}

func TestCmdStatusStepInput_PrintsEachAttempt(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	client := &stepInputMockClient{resp: &pb.GetStepInputsResponse{
		RunId: "a3f7-develop", StepName: "implement",
		Inputs: []*pb.StepInputEntry{
			{Attempt: 1, Kind: "prompt", Content: "Original prompt\n", CreatedAt: "2026-10-08T10:00:00Z"},
			{Attempt: 2, Kind: "prompt", Content: "Retry prompt", CreatedAt: "2026-10-08T10:05:00Z"},
		},
	}}
	var out bytes.Buffer
	cmdStatusStepInput(context.Background(), client, &out, "a3f7:develop:implement")

	if client.gotID != "a3f7:develop:implement" {
		t.Errorf("sent id %q; the CLI must pass the step id through unparsed", client.gotID)
	}
	got := out.String()
	want := "=== a3f7-develop:implement · attempt 1 · prompt · 2026-10-08T10:00:00Z ===\n" +
		"Original prompt\n" +
		"\n" +
		"=== a3f7-develop:implement · attempt 2 · prompt · 2026-10-08T10:05:00Z ===\n" +
		"Retry prompt\n"
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestCmdStatusStepInput_NoneRecorded(t *testing.T) {
	client := &stepInputMockClient{resp: &pb.GetStepInputsResponse{RunId: "a3f7-develop", StepName: "test"}}
	var out bytes.Buffer
	cmdStatusStepInput(context.Background(), client, &out, "a3f7:develop:test")
	if !strings.Contains(out.String(), "No input recorded for step test of run a3f7-develop") {
		t.Errorf("output: %q", out.String())
	}
}
