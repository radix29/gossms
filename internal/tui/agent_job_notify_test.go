package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// notifyConditionItems and notifyConditionLevels are parallel slices (labels
// and msdb codes). Round trips always agree with themselves, so a swapped pair
// ("When the job fails" writing NotifyOnComplete) would read back fine while
// e-mailing on every success; this pins each label to its code.
//
// A length mismatch hides an option or panics notifyConditionLevels[Selected()]
// mid-apply.
//
// Used by both Job Properties Notifications and New Job.
func TestNotifyConditionLabelsMatchTheirLevels(t *testing.T) {
	want := map[string]gosmo.NotifyLevel{
		"When the job succeeds":  gosmo.NotifyOnSuccess,
		"When the job fails":     gosmo.NotifyOnFailure,
		"When the job completes": gosmo.NotifyOnComplete,
	}
	checkLen(t, "notifyConditionItems", len(notifyConditionItems), len(notifyConditionLevels))
	for i, label := range notifyConditionItems {
		if got := notifyConditionLevels[i]; got != want[label] {
			t.Errorf("%q writes NotifyLevel %d, want %d", label, got, want[label])
		}
	}

	// notifyConditionIndex, the display half, searches levels and returns a
	// label position.
	for i, level := range notifyConditionLevels {
		if got := notifyConditionIndex(level); got != i {
			t.Errorf("notifyConditionIndex(%d) = %d, want %d (%q)", level, got, i, notifyConditionItems[i])
		}
	}

	// NotifyNever has no label (the E-mail checkbox turns notification off), so
	// it falls back to "When the job fails", the safe default shown when the
	// box is ticked.
	if got := notifyConditionIndex(gosmo.NotifyNever); notifyConditionItems[got] != "When the job fails" {
		t.Errorf("a job with no e-mail level shows %q, want \"When the job fails\"", notifyConditionItems[got])
	}
}
