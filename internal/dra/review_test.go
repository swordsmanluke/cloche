package dra

import "testing"

func TestReviewSecondRulingOnSameQuestionNotDropped(t *testing.T) {
	in := NewInbox()
	dir := t.TempDir()
	in.Add(dir, "a", Event{Kind: KindRuling, Subject: "q1", Text: "use option A"})
	in.Add(dir, "a", Event{Kind: KindRuling, Subject: "q1", Text: "actually, option B"})
	if got := len(in.Take(dir, "a")); got != 2 {
		t.Fatalf("want both rulings delivered, got %d (second ruling with different text was dropped)", got)
	}
}
