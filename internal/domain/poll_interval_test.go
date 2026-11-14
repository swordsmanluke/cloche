package domain

import (
	"strings"
	"testing"
)

func pollWorkflow(interval string) *Workflow {
	return &Workflow{Name: "w", Steps: map[string]*Step{
		"wait": {Name: "wait", Type: StepTypePoll, Config: map[string]string{"poll": "true", "interval": interval}},
	}}
}

func TestValidateConfig_PollIntervalFloor(t *testing.T) {
	warns := pollWorkflow("100ms").ValidateConfig()
	if len(warns) != 1 || !strings.Contains(warns[0], `"wait"`) || !strings.Contains(warns[0], "below the minimum poll interval "+MinPollInterval.String()) {
		t.Fatalf("want one below-minimum warning naming the step, got %v", warns)
	}
	for _, ok := range []string{"30s", MinPollInterval.String()} {
		if w := pollWorkflow(ok).ValidateConfig(); len(w) != 0 {
			t.Errorf("interval %s: unexpected warnings %v", ok, w)
		}
	}
}
