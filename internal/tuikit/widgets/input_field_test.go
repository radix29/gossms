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

func runeKey(r rune, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, string(r), mod)
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

// TestInputFieldHandleKeyReportsUnhandledKeys pins down that a focused
// field doesn't swallow keys it doesn't act on (Up/Down, Tab/Backtab, Esc,
// Enter): a caller like propsheet.Form relies on the false return to fall
// through to focus-cycling, or the field becomes a keyboard trap.
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

// A drag belongs to the field that claimed its press, until the release.
// Hit-testing every motion instead froze the selection the moment the
// pointer left the box, so dragging past the end of a term stopped
// extending it — invariant 1 in ARCHITECTURE.md § The mouseDragging idiom.
func TestInputFieldDragContinuesOutsideTheBox(t *testing.T) {
	f := NewInputField("Find: ", 20, false)
	f.SetBounds(4, 6)
	f.SetValue("abcdefgh")

	ix, y := f.InputX(), 6
	press := tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)
	if !f.HandleMouse(press) {
		t.Fatal("the press inside the box was refused — test premise is wrong")
	}

	// Two rows below the field, where HitTest fails.
	drag := tcell.NewEventMouse(ix+6, y+2, tcell.Button1, tcell.ModNone)
	if f.HitTest(ix+6, y+2) {
		t.Fatal("the drag point is still inside the box — test premise is wrong")
	}
	if !f.HandleMouse(drag) {
		t.Fatal("the field refused motion it owns the gesture for")
	}
	if got := f.SelectedText(); got != "abcde" {
		t.Errorf("SelectedText() = %q, want %q — the drag stopped at the box edge", got, "abcde")
	}

	// The release ends it, and the next off-rect press is refused again.
	f.HandleMouse(tcell.NewEventMouse(ix+6, y+2, tcell.ButtonNone, tcell.ModNone))
	if f.HandleMouse(drag) {
		t.Error("an off-rect press was accepted after the gesture ended")
	}
}

// A disabled field must refuse both keys and clicks, and must draw
// differently. Refusing input while looking identical to a live field is the
// worse of the two failures: the user gets no signal at all, only a box that
// ignores them.
func TestDisabledInputFieldRefusesInput(t *testing.T) {
	f := NewInputField("Schema: ", 20, false)
	f.SetBounds(4, 6)
	f.SetValue("dbo")
	f.Focus(true)
	f.SetEnabled(false)

	if f.Enabled() {
		t.Fatal("SetEnabled(false) did not take")
	}
	if f.HandleKey(runeKey('x', tcell.ModNone)) {
		t.Error("a disabled field consumed a keypress")
	}
	if got := f.Value(); got != "dbo" {
		t.Errorf("Value() = %q, want %q — a disabled field was edited", got, "dbo")
	}

	ix, y := f.InputX(), 6
	if f.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.Button1, tcell.ModNone)) {
		t.Error("a disabled field claimed a press")
	}
	if f.HandleMouse(tcell.NewEventMouse(ix+1, y, tcell.ButtonNone, tcell.ModNone)) {
		t.Error("a disabled field reported handling a release it never latched")
	}

	// Re-enabling restores both halves.
	f.SetEnabled(true)
	if !f.HandleKey(runeKey('x', tcell.ModNone)) {
		t.Error("a re-enabled field still refuses keys")
	}
	if got := f.Value(); got != "dbox" {
		t.Errorf("Value() = %q, want %q", got, "dbox")
	}
}

// The disabled look has to actually differ from the live one — the point of
// the flag is that it is visible.
func TestDisabledInputFieldDrawsDifferently(t *testing.T) {
	draw := func(enabled bool) map[[2]int]tcell.Style {
		f := NewInputField("Schema: ", 8, false)
		f.SetBounds(0, 0)
		f.SetValue("dbo")
		f.SetEnabled(enabled)
		s := newFieldScreen(40, 3)
		f.Draw(s)
		return s.styles
	}
	on, off := draw(true), draw(false)
	same := true
	for pos, st := range on {
		if off[pos] != st {
			same = false
			break
		}
	}
	if same {
		t.Error("a disabled field drew exactly like an enabled one")
	}
}
