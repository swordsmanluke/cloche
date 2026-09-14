package docker

import (
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
)

func TestStartedSuccessfully(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name   string
		status *ports.ContainerStatus
		want   bool
	}{
		{
			name:   "running",
			status: &ports.ContainerStatus{Running: true},
			want:   true,
		},
		{
			name:   "exited zero after running (fast-exiting command)",
			status: &ports.ContainerStatus{Running: false, ExitCode: 0, FinishedAt: now},
			want:   true,
		},
		{
			name:   "exited nonzero",
			status: &ports.ContainerStatus{Running: false, ExitCode: 1, FinishedAt: now},
			want:   false,
		},
		{
			name:   "created but never started (zero exit code, no finish time)",
			status: &ports.ContainerStatus{Running: false, ExitCode: 0, FinishedAt: time.Time{}},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, startedSuccessfully(tc.status))
		})
	}
}
