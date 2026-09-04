package tui

import (
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// The New Audit dialog's two parallel tables. Both are the round-trip trap
// CLAUDE.md names — a label list beside a value list, where swapping one
// entry leaves the dialog reading itself back consistently and writing the
// wrong TO clause — and neither had a test, though the declaration said one
// existed.

// TestAuditDestinationLabelsAndValuesArePaired pins the destination pair by
// name. The lists are shared with Audit Properties, whose own pairing test
// covers only the on-failure table.
func TestAuditDestinationLabelsAndValuesArePaired(t *testing.T) {
	if len(auditDestinationItems) != len(auditDestinationValues) {
		t.Fatalf("%d labels against %d values", len(auditDestinationItems), len(auditDestinationValues))
	}
	for label, want := range map[string]string{
		"File":            gosmo.AuditToFile,
		"Application Log": gosmo.AuditToApplicationLog,
		"Security Log":    gosmo.AuditToSecurityLog,
	} {
		i := slices.Index(auditDestinationItems, label)
		if i < 0 {
			t.Fatalf("no %q item", label)
		}
		if auditDestinationValues[i] != want {
			t.Errorf("%q writes %q, want %q", label, auditDestinationValues[i], want)
		}
	}
}

// TestAuditFileCountLabelsMatchTheirConstants pins the other half of the same
// hazard: here the "values" are the index constants both pages switch on, so a
// reordered label list would send MAX_FILES where the user asked for rollover
// files — and the count field would still read back the number they typed.
func TestAuditFileCountLabelsMatchTheirConstants(t *testing.T) {
	for label, want := range map[string]int{
		"Unlimited rollover files": auditFileCountUnlimited,
		"Rollover files":           auditFileCountRollover,
		"Maximum files":            auditFileCountMax,
	} {
		i := slices.Index(auditFileCountItems, label)
		if i < 0 {
			t.Fatalf("no %q item", label)
		}
		if i != want {
			t.Errorf("%q is index %d, want %d", label, i, want)
		}
	}
	if len(auditFileCountItems) != auditFileCountMax+1 {
		t.Errorf("%d items against %d constants", len(auditFileCountItems), auditFileCountMax+1)
	}
}
