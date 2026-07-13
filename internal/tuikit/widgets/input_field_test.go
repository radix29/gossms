package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestInputField(value string) *InputField {
	f := NewInputField("", 40, false)
	f.Focus(true)
	f.SetValue(value)
	return f
}

func key(k tcell.Key, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, "", mod)
}

func TestInputFieldSelectAll(t *testing.T) {
	f := newTestInputField("hello")
	f.HandleKey(key(tcell.KeyCtrlA, tcell.ModNone))
	if !f.HasSelection() {
		t.Fatal("Ctrl+A: expected a selection")
	}
	if got := f.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText() = %q, want %q", got, "hello")
	}
}

func TestInputFieldWordNavigation(t *testing.T) {
	f := newTestInputField("foo.bar baz")
	f.cursor = 0

	want := []int{3, 4, 7, 11}
	for _, w := range want {
		f.HandleKey(key(tcell.KeyRight, tcell.ModCtrl))
		if f.cursor != w {
			t.Fatalf("Ctrl+Right: cursor = %d, want %d", f.cursor, w)
		}
	}
	wantBack := []int{8, 4, 3, 0}
	for _, w := range wantBack {
		f.HandleKey(key(tcell.KeyLeft, tcell.ModCtrl))
		if f.cursor != w {
			t.Fatalf("Ctrl+Left: cursor = %d, want %d", f.cursor, w)
		}
	}
}

func TestInputFieldWordDelete(t *testing.T) {
	f := newTestInputField("foo bar baz")
	f.cursor = len(f.value)
	f.HandleKey(key(tcell.KeyBackspace, tcell.ModCtrl))
	if got := f.Value(); got != "foo bar " {
		t.Fatalf("Ctrl+Backspace = %q, want %q", got, "foo bar ")
	}

	f2 := newTestInputField("foo bar baz")
	f2.cursor = 0
	f2.HandleKey(key(tcell.KeyDelete, tcell.ModCtrl))
	if got := f2.Value(); got != " bar baz" {
		t.Fatalf("Ctrl+Delete = %q, want %q", got, " bar baz")
	}
}

// TestInputFieldSelectionDeletedByBackspace is a regression test: selecting
// text then pressing Backspace/Delete must actually remove it (see the
// identical fix and rationale in controls.Editor).
func TestInputFieldSelectionDeletedByBackspace(t *testing.T) {
	f := newTestInputField("abcdef")
	f.cursor = 0
	for i := 0; i < 3; i++ {
		f.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	if !f.HasSelection() || f.SelectedText() != "abc" {
		t.Fatalf("setup: SelectedText() = %q, want %q", f.SelectedText(), "abc")
	}
	f.HandleKey(key(tcell.KeyBackspace, tcell.ModNone))
	if got := f.Value(); got != "def" {
		t.Fatalf("Backspace over selection = %q, want %q", got, "def")
	}

	f2 := newTestInputField("abcdef")
	f2.cursor = 0
	for i := 0; i < 3; i++ {
		f2.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	f2.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
	if got := f2.Value(); got != "def" {
		t.Fatalf("Delete over selection = %q, want %q", got, "def")
	}
}

// TestInputFieldHandleKeyReportsUnhandledKeys is a regression test: a
// focused field must not swallow keys it doesn't act on (Up/Down, Tab/
// Backtab, Esc, Enter) — a caller like propsheet.Form relies on the false
// return to fall through to focus-cycling instead of the field becoming a
// keyboard trap.
func TestInputFieldHandleKeyReportsUnhandledKeys(t *testing.T) {
	f := newTestInputField("hello")
	for _, k := range []tcell.Key{tcell.KeyUp, tcell.KeyDown, tcell.KeyTab, tcell.KeyBacktab, tcell.KeyEscape, tcell.KeyEnter} {
		if f.HandleKey(key(k, tcell.ModNone)) {
			t.Fatalf("HandleKey(%v) = true, want false (unhandled)", k)
		}
	}
	// Sanity check: keys it does act on are still consumed.
	for _, k := range []tcell.Key{tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyEnd, tcell.KeyCtrlA, tcell.KeyBackspace} {
		if !f.HandleKey(key(k, tcell.ModNone)) {
			t.Fatalf("HandleKey(%v) = false, want true (handled)", k)
		}
	}
}

func TestInputFieldCutPasteRoundTrip(t *testing.T) {
	f := newTestInputField("hello world")
	f.cursor = 0
	for i := 0; i < 5; i++ {
		f.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	cut := f.Cut()
	if cut != "hello" {
		t.Fatalf("Cut() = %q, want %q", cut, "hello")
	}
	if got := f.Value(); got != " world" {
		t.Fatalf("after Cut(): Value() = %q, want %q", got, " world")
	}
	f.Paste("hello")
	if got := f.Value(); got != "hello world" {
		t.Fatalf("after Paste(): Value() = %q, want %q", got, "hello world")
	}
}
