// Package signal defines cloche's feedback-signal format (a source reports a
// problem), validates it, manages the per-project bearer token that guards the
// intake endpoint, and coalesces bursts of identical signals into one handler
// run. See docs/plans/2026-11-10-autonomous-loop-design.md, Part 4.
package signal

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Severity values a signal may carry.
const (
	SeverityLow    = "low"
	SeverityHigh   = "high"
	SeverityUrgent = "urgent"
)

// Field length limits. Evidence is log text and may be large; the rest are
// identifiers or one-liners.
const (
	MaxSourceLen   = 128
	MaxKeyLen      = 512
	MaxTitleLen    = 300
	MaxDomainLen   = 64
	MaxEvidenceLen = 64 * 1024
	// MaxBodyBytes bounds a whole request body.
	MaxBodyBytes = 128 * 1024
)

var (
	sourceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	domainRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Signal is one feedback event in cloche's signal format.
type Signal struct {
	Source   string `json:"source"`
	Key      string `json:"key"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Evidence string `json:"evidence,omitempty"`
	Domain   string `json:"domain,omitempty"`
}

// ValidateSource checks a signal source name.
func ValidateSource(source string) error {
	if source == "" {
		return fmt.Errorf("source is required")
	}
	if len(source) > MaxSourceLen || !sourceRe.MatchString(source) {
		return fmt.Errorf("source must be 1-%d characters of letters, digits, '.', '_' or '-'", MaxSourceLen)
	}
	return nil
}

// Validate checks every field and returns a message naming the first problem.
func (s *Signal) Validate() error {
	if err := ValidateSource(s.Source); err != nil {
		return err
	}
	if strings.TrimSpace(s.Key) == "" {
		return fmt.Errorf("key is required")
	}
	if len(s.Key) > MaxKeyLen {
		return fmt.Errorf("key must be at most %d bytes", MaxKeyLen)
	}
	if s.Severity == "" {
		return fmt.Errorf("severity is required")
	}
	switch s.Severity {
	case SeverityLow, SeverityHigh, SeverityUrgent:
	default:
		return fmt.Errorf("severity must be one of low, high, urgent")
	}
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(s.Title) > MaxTitleLen {
		return fmt.Errorf("title must be at most %d bytes", MaxTitleLen)
	}
	if strings.ContainsAny(s.Title, "\r\n") {
		return fmt.Errorf("title must be a single line")
	}
	if len(s.Evidence) > MaxEvidenceLen {
		return fmt.Errorf("evidence must be at most %d bytes", MaxEvidenceLen)
	}
	if s.Domain != "" && (len(s.Domain) > MaxDomainLen || !domainRe.MatchString(s.Domain)) {
		return fmt.Errorf("domain must be 1-%d characters of letters, digits, '.', '_' or '-'", MaxDomainLen)
	}
	for name, v := range map[string]string{"key": s.Key, "title": s.Title, "evidence": s.Evidence} {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return fmt.Errorf("%s must be valid UTF-8 text", name)
		}
	}
	return nil
}

// Priority maps severity to the tracker's 0 (most urgent) – 4 priority scale.
func Priority(severity string) string {
	switch severity {
	case SeverityUrgent:
		return "0"
	case SeverityHigh:
		return "1"
	default:
		return "3"
	}
}

// Record is a stored signal.
type Record struct {
	ID         int64
	ProjectDir string
	Signal     Signal
	ReceivedAt time.Time
}
