package scan

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// NextPass reads the pass cursor at cursorPath — the index of the next pass
// to run, written by AdvancePass — returning it alongside total. A missing
// or unreadable cursor means the first pass.
func NextPass(cursorPath string, total int) (next, totalOut int) {
	data, err := os.ReadFile(cursorPath)
	if err != nil {
		return 0, total
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 0 {
		return 0, total
	}
	return n, total
}

// AdvancePass records that passes before index next have run.
func AdvancePass(cursorPath string, next int) error {
	return os.WriteFile(cursorPath, []byte(strconv.Itoa(next)+"\n"), 0o644)
}

// AppendPassSummary records one pass's apply-reconcile summary line
// ("created N, superseded N, merged N, dropped N") against its repo, for
// AggregatePassSummaries.
func AppendPassSummary(path, repo, summary string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\n", repo, summary)
	return err
}

var summaryCounts = regexp.MustCompile(`created (\d+), superseded (\d+), merged (\d+), dropped (\d+)`)

// AggregatePassSummaries sums the per-pass summaries AppendPassSummary wrote
// into one "created N, superseded N, merged N, dropped N" line — the same
// shape a single pass prints, so the commit step's summary matching is
// unchanged. Returns "" when the file is missing or holds no summaries.
func AggregatePassSummaries(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var totals [4]int
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		m := summaryCounts.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found = true
		for i := range totals {
			n, _ := strconv.Atoi(m[i+1])
			totals[i] += n
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("created %d, superseded %d, merged %d, dropped %d", totals[0], totals[1], totals[2], totals[3])
}
