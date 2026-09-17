// Package durationfmt implements the one shared rule for rendering a
// time.Duration as a compact human-readable string, used by both the CLI
// (cmd/cloche) and the web handler (internal/adapters/web) so a long-running
// poll task reads as "3d4h" instead of overflowing to "2065h12m".
package durationfmt

import (
	"fmt"
	"time"
)

// Style controls formatting knobs that differ between call sites while all
// of them share the same hour/day rollover rule.
type Style struct {
	// ShowSeconds includes seconds alongside minutes in the sub-hour band
	// ("12m5s") instead of just minutes ("12m") — set only by callers whose
	// existing format already showed seconds at that resolution.
	ShowSeconds bool
	// Sep is inserted between the two rendered units in a two-unit band —
	// "" for the CLI's "6h12m", " " for the console's "6h 12m".
	Sep string
}

// Format renders d as a compact duration string, rolling hours into days
// once d reaches 24h and dropping to days-only past 7 days:
//
//	< 60s:  "39s"
//	< 1h:   "12m" (or "12m5s"/"12m 5s" with Style.ShowSeconds)
//	< 24h:  "6h12m" (or "6h 12m" with a space Sep)
//	< 7d:   "3d4h" (minutes dropped)
//	>= 7d:  "12d" (hours dropped)
func Format(d time.Duration, style Style) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int(d.Seconds())
	if totalSeconds < 60 {
		return fmt.Sprintf("%ds", totalSeconds)
	}
	totalMinutes := totalSeconds / 60
	if totalMinutes < 60 {
		if style.ShowSeconds {
			return fmt.Sprintf("%dm%s%ds", totalMinutes, style.Sep, totalSeconds%60)
		}
		return fmt.Sprintf("%dm", totalMinutes)
	}
	totalHours := totalMinutes / 60
	if totalHours < 24 {
		return fmt.Sprintf("%dh%s%dm", totalHours, style.Sep, totalMinutes%60)
	}
	totalDays := totalHours / 24
	if totalDays < 7 {
		return fmt.Sprintf("%dd%s%dh", totalDays, style.Sep, totalHours%24)
	}
	return fmt.Sprintf("%dd", totalDays)
}
