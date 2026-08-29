package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// notifyConditionItems and notifyConditionLevels are the same shape as the
// schedule dropdowns TestScheduleDropdownLabelsMatchTheirCodes pins: parallel
// slices indexed together, one holding the labels the user picks from and the
// other the msdb codes written for them. Every reader goes through both halves
// — notifyConditionIndex to display, notifyConditionLevels[Selected()] to
// write — so a round trip through the page agrees with itself whatever is in
// the tables. Swap the last two entries and "When the job fails" both displays
// for and writes NotifyOnComplete: the job then e-mails its operator on every
// success too, and the page still reads back exactly what it wrote.
//
// The length check is the other half. A label added to one slice only either
// hides an option or, on the value side being short, panics
// notifyConditionLevels[Selected()] on the last option — in an apply, after
// other statements have already run.
//
// Both the Job Properties Notifications page and the New Job dialog index these
// same two slices, so this covers both.
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

	// notifyConditionIndex is the display half, and is the one place the two
	// slices are not simply indexed together — it searches the levels and
	// returns a position into the labels.
	for i, level := range notifyConditionLevels {
		if got := notifyConditionIndex(level); got != i {
			t.Errorf("notifyConditionIndex(%d) = %d, want %d (%q)", level, got, i, notifyConditionItems[i])
		}
	}

	// NotifyNever has no label of its own — the E-mail checkbox is what turns
	// notification off — so it falls back to a real option. That fallback is
	// what a job with e-mail disabled shows the moment the user ticks the box,
	// and "when the job fails" is the only safe default to offer.
	if got := notifyConditionIndex(gosmo.NotifyNever); notifyConditionItems[got] != "When the job fails" {
		t.Errorf("a job with no e-mail level shows %q, want \"When the job fails\"", notifyConditionItems[got])
	}
}
