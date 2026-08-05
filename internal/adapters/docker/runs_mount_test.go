package docker

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/ports"
)

// The .cloche/runs/<run-id> bind mount must source from the live project dir
// whenever one is given: ProjectDir may be a clean-snapshot clone, which has
// no runtime state (no task_prompt.md) and is deleted right after container
// start. Regression test for the v3.19.0 prompt-delivery bug.
func TestRunsMountRoot(t *testing.T) {
	cases := []struct {
		name string
		cfg  ports.ContainerConfig
		want string
	}{
		{
			name: "live dir wins over snapshot seed dir",
			cfg:  ports.ContainerConfig{ProjectDir: "/tmp/cloche-snapshot-abc", HostProjectDir: "/home/u/project"},
			want: "/home/u/project",
		},
		{
			name: "falls back to ProjectDir when live dir unset",
			cfg:  ports.ContainerConfig{ProjectDir: "/home/u/project"},
			want: "/home/u/project",
		},
		{
			name: "empty for resume containers with no project dir",
			cfg:  ports.ContainerConfig{},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runsMountRoot(tc.cfg); got != tc.want {
				t.Errorf("runsMountRoot() = %q, want %q", got, tc.want)
			}
		})
	}
}
