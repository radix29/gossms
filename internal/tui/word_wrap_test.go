package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// Alt+Z (and Alt+Shift+Z) toggles Word Wrap app-wide: every open query editor
// follows, and a panel opened afterwards inherits the setting. Plain 'z' is
// text, and Ctrl+Alt+Z (AltGr on many layouts) must not toggle either.
func TestAltZTogglesWordWrapForEveryQueryPanel(t *testing.T) {
	a := newKeyTestApp(t)
	a.newQueryPanel()
	first := a.activeQueryPanel()
	a.newQueryPanel()
	second := a.activeQueryPanel()

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, "z", tcell.ModAlt))
	if !a.wordWrap || !first.editor.WrapMode() || !second.editor.WrapMode() {
		t.Fatalf("after Alt+Z: wordWrap=%v first=%v second=%v, want all on",
			a.wordWrap, first.editor.WrapMode(), second.editor.WrapMode())
	}

	a.newQueryPanel()
	third := a.activeQueryPanel()
	if !third.editor.WrapMode() {
		t.Fatal("a query panel opened with Word Wrap on did not inherit it")
	}

	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, "z", tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, "z", tcell.ModAlt|tcell.ModCtrl),
	} {
		a.handleKey(ev)
		if !a.wordWrap {
			t.Fatalf("%v toggled Word Wrap off", ev.Name())
		}
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, "Z", tcell.ModAlt|tcell.ModShift))
	if a.wordWrap || first.editor.WrapMode() {
		t.Fatalf("after Alt+Shift+Z: wordWrap=%v first=%v, want off", a.wordWrap, first.editor.WrapMode())
	}
	if got := third.editor.Text(); strings.ContainsAny(got, "Z") || !strings.Contains(got, "z") {
		t.Fatalf("editor text = %q, want the plain 'z' typed and no 'Z' from Alt+Shift+Z", got)
	}
}
