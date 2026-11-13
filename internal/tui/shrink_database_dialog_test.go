package tui

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/propsheet"
)

// The shrink dialogs only script, so what these tests pin is the statement
// Script would put in the query window — and that the page takes its options
// from the rows the user changed, by label.

func shrinkDatabaseTestDialog(t *testing.T, sc *db.ServerConn) *ShrinkDatabaseDialog {
	t.Helper()
	d := &ShrinkDatabaseDialog{dbName: "App'DB"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&shrinkDatabasePrefetch{allocatedKB: 144 * 1024, freeKB: 54170})
	return d
}

// shrinkScript runs a script-only dialog's preflight and apply under
// gosmo.WithScript, as Script does, and returns what it captured.
func shrinkScript(t *testing.T, preflight func() error, apply propApply) string {
	t.Helper()
	if err := preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := apply(ctx); err != nil {
		t.Fatalf("apply under WithScript: %v", err)
	}
	return script.String()
}

func TestShrinkDatabaseUntickedScriptsTheBareStatement(t *testing.T) {
	sc, inst := newGConn(t)
	d := shrinkDatabaseTestDialog(t, sc)

	got := shrinkScript(t, d.preflight, d.applyFns[0])
	// SSMS's own Script output with the box unticked (2026-10-21).
	if !strings.Contains(got, "DBCC SHRINKDATABASE (N'App''DB')") || strings.Contains(got, "TRUNCATEONLY") {
		t.Errorf("script = %q, want the bare DBCC SHRINKDATABASE (N'App''DB')", got)
	}
	if !strings.Contains(got, "USE [App'DB]") {
		t.Errorf("script = %q, want it to start in the database", got)
	}
	if n := len(inst.Statements()); n != 0 {
		t.Errorf("Script sent %d statements to the server, want none: %q", n, inst.Statements())
	}
}

func TestShrinkDatabaseReorganizeSendsThePercent(t *testing.T) {
	sc, _ := newGConn(t)
	d := shrinkDatabaseTestDialog(t, sc)
	f := d.forms[0]

	pct := textRow(t, f, "Max free space after shrinking")
	if pct.Enabled() {
		t.Fatal("the percentage is editable before Reorganize is ticked")
	}
	editCheck(t, f, "Reorganize files before releasing unused space", true)
	if !pct.Enabled() {
		t.Fatal("ticking Reorganize left the percentage disabled")
	}
	editText(t, f, "Max free space after shrinking", "10")

	if got := shrinkScript(t, d.preflight, d.applyFns[0]); !strings.Contains(got, "DBCC SHRINKDATABASE (N'App''DB', 10)") {
		t.Errorf("script = %q, want DBCC SHRINKDATABASE (N'App''DB', 10)", got)
	}

	editText(t, f, "Max free space after shrinking", "100")
	if err := d.preflight(); err == nil {
		t.Error("preflight accepted 100%, which SHRINKDATABASE refuses")
	}
}

func TestShrinkDatabaseShowsSizesAsSSMSDoes(t *testing.T) {
	sc, _ := newGConn(t)
	d := shrinkDatabaseTestDialog(t, sc)
	for label, want := range map[string]string{
		"Currently allocated space": "144.00 MB",
		"Available free space":      "52.90 MB (36%)", // 36.7%, truncated
	} {
		if got := staticRowValue(t, d.forms[0], label); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

// The wait choice is mapped by name: a swapped pair of shrinkWaitValues would
// otherwise kill blockers when the user asked the shrink to stop itself.
func TestShrinkWaitChoicesMapByName(t *testing.T) {
	sc, _ := newGConn(t)
	for choice, want := range map[string]string{
		"Yes, then stop the shrink":   "WITH WAIT_AT_LOW_PRIORITY (ABORT_AFTER_WAIT = SELF)",
		"Yes, then kill the blockers": "WITH WAIT_AT_LOW_PRIORITY (ABORT_AFTER_WAIT = BLOCKERS)",
	} {
		d := shrinkDatabaseTestDialog(t, sc)
		editSelect(t, d.forms[0], "Wait at low priority", choice)
		if got := shrinkScript(t, d.preflight, d.applyFns[0]); !strings.Contains(got, want) {
			t.Errorf("%q scripted %q, want %s", choice, got, want)
		}
	}
	if len(shrinkWaitChoices) != len(shrinkWaitValues) {
		t.Errorf("%d wait choices but %d values", len(shrinkWaitChoices), len(shrinkWaitValues))
	}
}

// Before SQL Server 2022 the clause is a syntax error, so the row is not
// offered at all.
func TestShrinkWaitIsNotOfferedBefore2022(t *testing.T) {
	sc, _ := newGConnAtVersion(t, "15.0.4000.1")
	d := shrinkDatabaseTestDialog(t, sc)
	for _, r := range d.forms[0].Rows() {
		if sr, ok := r.(*propsheet.SelectRow); ok && sr.Label() == sheetLabel("Wait at low priority") {
			t.Fatal("SQL Server 2019 is offered WAIT_AT_LOW_PRIORITY")
		}
	}
	if got := shrinkScript(t, d.preflight, d.applyFns[0]); strings.Contains(got, "WAIT_AT_LOW_PRIORITY") {
		t.Errorf("script = %q, want no wait clause", got)
	}
}

// The cascade is offered on an ONLINE, writable database only: the dialogs
// read sizes through a USE, which an offline or restoring database refuses,
// and every DBCC SHRINK* fails on a read-only one.
func TestShrinkIsOfferedOnAnOnlineDatabaseOnly(t *testing.T) {
	a := newTestApp()
	sc, _ := newGConn(t)
	_, _, restoring, master := recoverTree(t, a, sc)

	item := findMenuItem(a.contextMenuItemsForNode(master), "Shrink")
	if item == nil {
		t.Fatal("an ONLINE database offers no Shrink")
	}
	if findMenuItem(item.Sub, "Database...") == nil || findMenuItem(item.Sub, "Files...") == nil {
		t.Errorf("Shrink offers %v, want Database... and Files...", item.Sub)
	}
	if findMenuItem(a.contextMenuItemsForNode(restoring), "Shrink") != nil {
		t.Error("a RESTORING database offers Shrink")
	}
	master.data.DBReadOnly = true
	if findMenuItem(a.contextMenuItemsForNode(master), "Shrink") != nil {
		t.Error("a read-only database offers Shrink")
	}
}

// staticRowValue reads a read-only row by its label.
func staticRowValue(t *testing.T, f *propsheet.Form, label string) string {
	t.Helper()
	for _, r := range f.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == sheetLabel(label) {
			return sr.Value()
		}
	}
	t.Fatalf("no static row labelled %q on this page", label)
	return ""
}
