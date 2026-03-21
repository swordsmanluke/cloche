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
	TaskID       string // task ID for runtime state paths (.cloche/runs/<task-id>/)
	AttemptID    string // attempt ID for unique container naming
	Cmd          []string // override container command; defaults to ["cloche-agent", WorkflowName]
	Prompt       string // prompt text to write into .cloche/runs/<task-id>/prompt.txt in container
	Interactive  bool   // allocate TTY and keep stdin open (-it flags)
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
	// Attach connects to a running container's stdin/stdout/stderr for
	// bidirectional I/O. Requires the container to have been started with
	// Interactive=true in its ContainerConfig.
	Attach(ctx context.Context, containerID string) (io.ReadWriteCloser, error)
}

// ImageEnsurer is an optional interface that a ContainerRuntime may implement
// to validate and rebuild container images when the project Dockerfile has
// changed since the last build.
type ImageEnsurer interface {
	EnsureImage(ctx context.Context, projectDir, image string) error
}

// ContainerCommitter is an optional interface for creating an image from a
// stopped container's filesystem state. Used for resume: the committed image
// preserves all step outputs and workspace changes from the failed run.
type ContainerCommitter interface {
	Commit(ctx context.Context, containerID string) (imageID string, err error)
}

// ContainerCopier is an optional interface for copying files into a container.
// Used to inject updated scripts before resuming a run.
type ContainerCopier interface {
	CopyTo(ctx context.Context, containerID string, srcPath, dstPath string) error
}

// TerminalResizer is an optional interface for resizing the pseudo-TTY of an
// interactive container. Used to forward SIGWINCH events from the CLI.
type TerminalResizer interface {
	ResizeTerminal(ctx context.Context, containerID string, rows, cols int) error
}
