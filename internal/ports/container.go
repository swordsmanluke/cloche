package ports

import (
	"context"
	"io"
	"time"
)

type ContainerStatus struct {
	Running    bool
	ExitCode   int
	FinishedAt time.Time
}

type ContainerConfig struct {
	Image        string
	WorkflowName string
	ProjectDir   string
	NetworkAllow []string
	RunID        string
	Cmd          []string          // override container command; defaults to ["cloche-agent", WorkflowName]
	EnvVars      map[string]string // additional environment variables to set in the container
}

type ContainerRuntime interface {
	Start(ctx context.Context, cfg ContainerConfig) (containerID string, err error)
	Stop(ctx context.Context, containerID string) error
	AttachOutput(ctx context.Context, containerID string) (io.ReadCloser, error)
	Wait(ctx context.Context, containerID string) (exitCode int, err error)
	CopyFrom(ctx context.Context, containerID string, srcPath, dstPath string) error
	Logs(ctx context.Context, containerID string) (string, error)
	Remove(ctx context.Context, containerID string) error
	Inspect(ctx context.Context, containerID string) (*ContainerStatus, error)
}

// ImageEnsurer is an optional interface that a ContainerRuntime may implement
// to validate and rebuild container images when the project Dockerfile has
// changed since the last build.
type ImageEnsurer interface {
	EnsureImage(ctx context.Context, projectDir, image string) error
}
