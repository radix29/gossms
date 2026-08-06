package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// hdr builds a backup header at a given device position.
func hdr(pos int, db string) *gosmo.BackupHeader {
	return &gosmo.BackupHeader{Position: pos, DatabaseName: db}
}

// TestRestoreFileNumberSingleSet pins that a device holding one backup set
// emits no WITH FILE clause — SQL Server's own default is the first set, so
// the clause would be noise on the overwhelmingly common case.
func TestRestoreFileNumberSingleSet(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "AppDB")}}
	if got := d.restoreFileNumber(); got != 0 {
		t.Errorf("restoreFileNumber() = %d, want 0 for a single-set device", got)
	}
}

// TestRestoreFileNumberFollowsSelection is the regression test for the
// shipped behaviour: the dialog always restored backup set 1 no matter which
// set the inspect view was showing, because RestoreOptions had no FILE = n
// and buildRestoreOptions read headers[0] unconditionally. A .bak written
// with NOINIT (full at 1, differential at 2) could not restore the
// differential at all.
func TestRestoreFileNumberFollowsSelection(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{
		hdr(1, "AppDB"), hdr(2, "AppDB"), hdr(3, "AppDB"),
	}}
	for _, want := range []int{1, 2, 3} {
		d.headerIdx = want - 1
		if got := d.restoreFileNumber(); got != want {
			t.Errorf("headerIdx %d: restoreFileNumber() = %d, want %d", d.headerIdx, got, want)
		}
	}
}

// TestRestoreFileNumberUsesPositionNotIndex pins that the number sent to SQL
// Server is the set's own Position, not its slice index. The two only
// coincide when the device's sets are contiguous from 1.
func TestRestoreFileNumberUsesPositionNotIndex(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(4, "AppDB"), hdr(7, "AppDB")}}
	d.headerIdx = 1
	if got := d.restoreFileNumber(); got != 7 {
		t.Errorf("restoreFileNumber() = %d, want 7 (the header's Position, not index+1)", got)
	}
}

// TestSelectHeaderClamps pins that arrowing past either end stays put rather
// than wrapping — holding an arrow must not cycle past the intended set.
func TestSelectHeaderClamps(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "A"), hdr(2, "A")}}
	d.selectHeader(-5)
	if d.headerIdx != 0 {
		t.Errorf("selectHeader(-5) = %d, want 0", d.headerIdx)
	}
	d.selectHeader(99)
	if d.headerIdx != 1 {
		t.Errorf("selectHeader(99) = %d, want 1", d.headerIdx)
	}
}

// TestSelectedHeaderSurvivesAStaleIndex pins the guard: headerIdx is UI
// state and headers is replaced by every Analyze, so a stale index must fall
// back rather than panic.
func TestSelectedHeaderSurvivesAStaleIndex(t *testing.T) {
	d := &RestoreDialog{headers: []*gosmo.BackupHeader{hdr(1, "AppDB")}, headerIdx: 9}
	if got := d.selectedHeader(); got == nil || got.DatabaseName != "AppDB" {
		t.Errorf("selectedHeader() = %v, want the first header", got)
	}
}

// TestSelectedHeaderOnNoHeaders pins the nil return. The stale-index guard
// above used to fall back to headers[0], which panics when there is no
// headers[0] — on the UI goroutine, where recoverPanic cannot catch it.
func TestSelectedHeaderOnNoHeaders(t *testing.T) {
	d := &RestoreDialog{}
	if got := d.selectedHeader(); got != nil {
		t.Errorf("selectedHeader() on no headers = %v, want nil", got)
	}
	d.selectHeader(3) // must not leave an index selectedHeader would index with
	if got := d.selectedHeader(); got != nil {
		t.Errorf("selectedHeader() after selectHeader = %v, want nil", got)
	}
	if got := d.restoreFileNumber(); got != 0 {
		t.Errorf("restoreFileNumber() on no headers = %d, want 0", got)
	}
}
