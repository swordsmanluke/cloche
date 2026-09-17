package durationfmt

import (
	"testing"
	"time"
)

func TestFormat_Boundaries(t *testing.T) {
	tests := []struct {
		name  string
		d     time.Duration
		style Style
		want  string
	}{
		{"59s", 59 * time.Second, Style{}, "59s"},
		{"60s", 60 * time.Second, Style{}, "1m"},
		{"59m59s", 59*time.Minute + 59*time.Second, Style{}, "59m"},
		{"1h", time.Hour, Style{}, "1h0m"},
		{"23h59m", 23*time.Hour + 59*time.Minute, Style{}, "23h59m"},
		{"24h", 24 * time.Hour, Style{}, "1d0h"},
		{"6d23h", 6*24*time.Hour + 23*time.Hour, Style{}, "6d23h"},
		{"7d", 7 * 24 * time.Hour, Style{}, "7d"},

		// ShowSeconds threads the sub-hour band, unaffected elsewhere.
		{"59s/showSeconds", 59 * time.Second, Style{ShowSeconds: true}, "59s"},
		{"60s/showSeconds", 60 * time.Second, Style{ShowSeconds: true}, "1m0s"},
		{"12m5s/showSeconds", 12*time.Minute + 5*time.Second, Style{ShowSeconds: true}, "12m5s"},
		{"1h/showSeconds", time.Hour, Style{ShowSeconds: true}, "1h0m"},

		// Sep inserts a space between the two rendered units (console style).
		{"6h12m/spaced", 6*time.Hour + 12*time.Minute, Style{Sep: " "}, "6h 12m"},
		{"3d4h/spaced", 3*24*time.Hour + 4*time.Hour, Style{Sep: " "}, "3d 4h"},
		{"12d/spaced", 12 * 24 * time.Hour, Style{Sep: " "}, "12d"},
		{"12m5s/spaced+seconds", 12*time.Minute + 5*time.Second, Style{ShowSeconds: true, Sep: " "}, "12m 5s"},

		// Negative durations clamp to zero rather than going negative.
		{"negative", -5 * time.Second, Style{}, "0s"},

		// Zero.
		{"zero", 0, Style{}, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.d, tt.style)
			if got != tt.want {
				t.Errorf("Format(%v, %+v) = %q, want %q", tt.d, tt.style, got, tt.want)
			}
		})
	}
}
