package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// A host's own ValueViewer works from the zero value, and passes every event
// on while hidden, so holding one costs a host nothing until it is shown.
func TestValueViewerStandalone(t *testing.T) {
	var v ValueViewer
	if v.OverlayActive() || v.HandleKey(tuikittest.Key(tcell.KeyDown)) ||
		v.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone)) {
		t.Fatal("a hidden viewer claimed input")
	}

	v.Show("Definition", "line one\nline two")
	scr := tuikittest.NewScreen(80, 24)
	v.DrawOverlay(scr)
	if !v.OverlayActive() || v.Title() != "Definition" || v.Text() != "line one\nline two" {
		t.Fatalf("after Show: active %v, title %q, text %q", v.OverlayActive(), v.Title(), v.Text())
	}
	if _, _, ok := scr.Find("Definition"); !ok {
		t.Error("the title was not drawn")
	}
	if _, _, ok := scr.Find("line two"); !ok {
		t.Error("the value was not drawn")
	}

	v.SelectAll()
	if got := v.SelectedText(); got != "line one\nline two" {
		t.Errorf("SelectedText() = %q after SelectAll", got)
	}
	if !v.HandleKey(tuikittest.Key(tcell.KeyEscape)) || v.OverlayActive() {
		t.Error("Escape did not close the viewer")
	}
	if v.HasSelection() || v.SelectedText() != "" {
		t.Error("a hidden viewer still reports its selection")
	}
}
