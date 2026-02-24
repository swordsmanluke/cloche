package ports

import (
	"context"
	"io"
)

type ContainerConfig struct {
	Image        string
	WorkflowName string
	ProjectDir   string
	NetworkAllow []string
	GitRemote    string
	RunID        string
	Cmd          []string // override container command; defaults to ["cloche-agent", WorkflowName]
}

type ContainerRuntime interface {
	Start(ctx context.Context, cfg ContainerConfig) (containerID string, err error)
	Stop(ctx context.Context, containerID string) error
	AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error)
	Wait(ctx context.Context, containerID string) (exitCode int, err error)
}
