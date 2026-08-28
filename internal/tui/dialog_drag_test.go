package tui

import (
	"io"
	"log"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// A text-selection drag belongs to the field that claimed its press until
// the release, wherever the pointer goes — invariant 1 in ARCHITECTURE.md
// § The mouseDragging idiom. widgets.InputField honours this itself, but
// only if its host forwards the off-rect motion; these three dialogs used to
// hit-test every Button1 first, so the selection froze at the box edge.
//
// The tests drive the fields' own coordinates rather than counting layout
// rows, so they don't have to be updated when a row moves.

// quietLog discards log output for the rest of the test — for the async
// fetches these dialogs start on Show, which panic against a bare
// ServerConn and are recovered with a logged stack.
func quietLog(t *testing.T) {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

// dragOutOf presses inside f at offset +1 into its box, then drags to
// (dragX, dragY) — which the caller must have chosen outside f — and reports
// what ended up selected. host is the dialog whose HandleMouse is under test.
func dragOutOf(t *testing.T, host interface {
	HandleMouse(*tcell.EventMouse) bool
}, f *widgets.InputField, dragX, dragY int) string {
	t.Helper()
	ix, y := f.InputX(), f.RectY()
	if !host.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the press inside the field was refused — test premise is wrong")
	}
	if f.HitTest(dragX, dragY) {
		t.Fatal("the drag point is still inside the field — test premise is wrong")
	}
	if !host.HandleMouse(tcell.NewEventMouse(dragX, dragY, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the dialog refused motion the field owns the gesture for")
	}
	return f.SelectedText()
}

func TestConnectDialogDragOutOfAFieldKeepsExtending(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewConnectDialog(a)
	d.Show()
	d.layoutFields()
	d.fServer.SetValue("abcdefgh")

	// Two rows below the server field: over fPort/ddAuth, which would
	// otherwise take the focus out from under the drag.
	ix, y := d.fServer.InputX(), d.fServer.RectY()
	if got := dragOutOf(t, d, d.fServer, ix+6, y+2); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}
	if d.focusIdx != 0 {
		t.Errorf("focusIdx = %d, want 0 — a widget below stole a gesture fServer owned", d.focusIdx)
	}

	// The release ends it wherever it lands, including outside the dialog,
	// where ConsumeOutsideClick would otherwise swallow it and strand the
	// latch into the next press.
	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.fServer.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

// A latch must not survive into the next showing (tuikit invariant 4): a
// dialog dismissed mid-drag would reopen routing every click to that field.
func TestConnectDialogShowClearsTheDragLatch(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewConnectDialog(a)
	d.Show()
	d.layoutFields()
	// Arm the latch the way a user does — a press inside the field — rather
	// than by assignment, so the test also pins that a press arms one at all.
	ix, y := d.fServer.InputX(), d.fServer.RectY()
	d.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone))
	if d.drag.Field() == nil {
		t.Fatal("the press did not arm a latch — test premise is wrong")
	}

	d.Show()
	if d.drag.Field() != nil {
		t.Error("Show left a drag latch armed from the previous showing")
	}
}

func TestBackupDialogDragOutOfDestKeepsExtending(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewBackupDialog(a)
	// show builds the widgets, and also kicks off the async database-list
	// fetch. Against a bare ServerConn that goroutine panics on the nil
	// gosmo.Server and recoverPanic logs the stack — expected, and muted so
	// it can't be mistaken for this test failing. Dropping the app's screen
	// makes recoverPanic's postAndWake a no-op rather than dereferencing the
	// fake's nil embedded tcell.Screen; the dialog kept its own reference
	// for its rect.
	quietLog(t)
	a.screen = nil
	d.show(&db.ServerConn{}, "testdb")
	// Mirrors backup_dialog_draw.go's fDest.SetBounds, which only runs under
	// Draw. Placed inside the dialog but clear of the button row.
	inner := d.InnerRect()
	d.fDest.SetBounds(inner.X+1, inner.Y+2)
	d.fDest.SetValue("abcdefgh")

	ix, y := d.fDest.InputX(), d.fDest.RectY()
	if got := dragOutOf(t, d, d.fDest, ix+6, y+2); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}

	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.fDest.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

func TestRestoreDialogDragOutOfTargetKeepsExtending(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewRestoreDialog(a)
	a.screen = nil // see the backup test above
	d.show(&db.ServerConn{}, "testdb")
	// Mirrors restore_dialog_draw.go's fTarget.SetBounds (Draw-only).
	inner := d.InnerRect()
	d.fTarget.SetBounds(inner.X+1, inner.Y+2)
	d.fTarget.SetValue("abcdefgh")

	ix, y := d.fTarget.InputX(), d.fTarget.RectY()
	if got := dragOutOf(t, d, d.fTarget, ix+6, y+2); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}

	// The release path this dialog gets wrong most easily: it runs ahead of
	// both ConsumeOutsideClick and the mode switch, either of which returns
	// early and would strand the latch.
	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.fTarget.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

// OptionsDialog routes to fMaxCellLen unconditionally, so its drag already
// worked — but its ButtonNone latch-reset list omitted the field, so a
// release outside the dialog (eaten by ConsumeOutsideClick) left it armed
// and the field swallowed the next press.
func TestOptionsDialogReleaseOutsideClearsTheFieldLatch(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40}
	d := NewOptionsDialog(a)
	d.Show()
	inner := d.InnerRect()
	d.fMaxCellLen.SetBounds(inner.X+1, inner.Y+1)

	ix, y := d.fMaxCellLen.InputX(), d.fMaxCellLen.RectY()
	if !d.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)) {
		t.Fatal("the press inside the field was refused — test premise is wrong")
	}
	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))

	if d.fMaxCellLen.HandleMouse(tcell.NewEventMouse(ix+6, y+9, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

// The two dialogs the drag invariant was never covered on. Both route through
// dialogs.FieldGesture now; before it they carried their own copy of the
// protocol, and a copy is only ever one edit away from hit-testing the motion.
func TestLogSearchDialogDragOutOfAFieldKeepsExtending(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40} // before the dialog: InitModal captures it
	d := NewLogSearchDialog(a)
	d.ShowLogSearch(gosmo.LogSearch{}, func(gosmo.LogSearch) {})
	d.layout() // Draw's job; the test isn't drawing
	d.fText1.SetValue("abcdefgh")

	// Two rows down, over fFrom/fTo, which would take the focus out from
	// under the drag if the motion were hit-tested.
	ix, y := d.fText1.InputX(), d.fText1.RectY()
	if got := dragOutOf(t, d, d.fText1, ix+6, y+2); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}
	if d.focusIdx != 0 {
		t.Errorf("focusIdx = %d, want 0 — a field below stole a gesture fText1 owned", d.focusIdx)
	}

	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.fText1.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

func TestFilterDialogDragOutOfAValueFieldKeepsExtending(t *testing.T) {
	d := showTestFilterDialogOnScreen(t, 100, 40)
	f := d.rows[0].value
	f.SetValue("abcdefgh")

	// Two rows down, over another row's operator dropdown — a widget that
	// answers a press and would swallow the motion.
	ix, y := f.InputX(), f.RectY()
	if got := dragOutOf(t, d, f, ix+6, y+2); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}
	if d.focusIdx != 1 {
		t.Errorf("focusIdx = %d, want 1 (row 0's value field) — a widget below stole the gesture", d.focusIdx)
	}

	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if f.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}

// FindReplaceDialog is the one the other five copied their comment from, and
// the only one of the seven with no drag test of its own until now.
func TestFindDialogDragOutOfAFieldKeepsExtending(t *testing.T) {
	a := newTestApp()
	a.screen = &fakeSizedScreen{w: 100, h: 40} // before the dialog: InitModal captures it
	a.findDialog = NewFindReplaceDialog(a)
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.editor.SetText("select 1")

	d := a.findDialog
	d.ShowReplace() // two fields, so there is somewhere below to drag to
	d.layout()
	d.fFind.SetValue("abcdefgh")

	ix, y := d.fFind.InputX(), d.fFind.RectY()
	if got := dragOutOf(t, d, d.fFind, ix+6, y+3); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}
	if d.focusIdx != 0 {
		t.Errorf("focusIdx = %d, want 0 — a widget below stole a gesture fFind owned", d.focusIdx)
	}

	d.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
	if d.drag.Field() != nil {
		t.Fatal("a release outside the dialog left the gesture latched")
	}
	if d.fFind.HandleMouse(tcell.NewEventMouse(ix+6, y+3, tcell.Button1, tcell.ModNone)) {
		t.Error("the field is still latched — its next off-rect press was accepted")
	}
}
