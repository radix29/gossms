package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newWrappedTestEditor returns an editor laid out narrow enough that text wraps,
// with the gutter hidden so a mouse x maps straight to a rune column.
func newWrappedTestEditor(text string, w, h int) *Editor {
	e := newTestEditor(text)
	e.SetGutterVisible(false)
	e.SetBounds(0, 0, w, h)
	e.SetWrapMode(true)
	return e
}

// A block (column) selection cannot survive into wrap mode: it reinterprets the
// anchor/cursor pair as a rectangle over fixed rune columns, which wrapping
// breaks, and handleMouseWrapped never touches selBlock — so no click in wrap
// mode would clear a flag the setter left behind.
func TestSetWrapModeClearsBlockSelection(t *testing.T) {
	e := newTestEditor("aaaa\nbbbb\ncccc")
	blockSelect(e, 0, 1, 2, 3)
	if got, want := e.SelectedText(), "aa\nbb\ncc"; got != want {
		t.Fatalf("block SelectedText() = %q, want %q", got, want)
	}

	e.SetWrapMode(true)

	if e.selBlock {
		t.Fatal("selBlock still set after entering wrap mode")
	}
	if got := e.SelectedText(); got != "" {
		t.Fatalf("SelectedText() = %q after mode switch, want the selection dropped", got)
	}
}

// Switching mode reinterprets scrollRow (visual rows vs. logical lines) and
// invalidates scrollCol, so both are reset rather than carried across.
func TestSetWrapModeResetsScrollState(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight")
	e.SetBounds(0, 0, 20, 3)
	e.scrollRow, e.scrollCol = 5, 7
	e.selecting, e.mouseDragging = true, true

	e.SetWrapMode(true)

	if e.scrollRow != 0 || e.scrollCol != 0 {
		t.Fatalf("scroll after mode switch = (%d,%d), want (0,0)", e.scrollRow, e.scrollCol)
	}
	if e.selecting || e.mouseDragging {
		t.Fatalf("selecting=%v mouseDragging=%v after mode switch, want both false", e.selecting, e.mouseDragging)
	}
}

// Setting the mode it already has is the shipped call shape — three call sites
// set it once at construction — and must not disturb anything.
func TestSetWrapModeSameValueIsNoOp(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight")
	e.SetBounds(0, 0, 20, 3)
	e.cursorRow, e.cursorCol = 4, 2
	e.scrollRow, e.scrollCol = 3, 1
	e.selecting = true
	e.selAnchorRow, e.selAnchorCol = 4, 0

	e.SetWrapMode(false) // already false

	if e.cursorRow != 4 || e.cursorCol != 2 {
		t.Fatalf("cursor = (%d,%d), want (4,2)", e.cursorRow, e.cursorCol)
	}
	if e.scrollRow != 3 || e.scrollCol != 1 {
		t.Fatalf("scroll = (%d,%d), want (3,1)", e.scrollRow, e.scrollCol)
	}
	if !e.selecting || e.selAnchorRow != 4 || e.selAnchorCol != 0 {
		t.Fatalf("selection dropped by a no-op SetWrapMode: selecting=%v anchor=(%d,%d)",
			e.selecting, e.selAnchorRow, e.selAnchorCol)
	}
}

// The wrapped press path goes through the same applyMousePress body as the
// unwrapped one, so Shift+Click extends from the pre-click cursor rather than
// starting a new selection. TestEditorShiftClickExtendsSelectionInWrapMode
// covers the same rule on text wide enough not to wrap; this one clicks on a
// second visual row, the derivation the wrapped branch keeps for itself.
func TestWrappedMouseShiftClickExtendsSelection(t *testing.T) {
	e := newWrappedTestEditor("hello world again", 11, 4)
	e.cursorRow, e.cursorCol = 0, 0

	// Row 1 of the wrapped display, one column in.
	e.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModShift))

	if !e.selecting {
		t.Fatal("Shift+Click in wrap mode did not start a selection")
	}
	if e.selAnchorRow != 0 || e.selAnchorCol != 0 {
		t.Fatalf("anchor = (%d,%d), want the pre-click cursor (0,0)", e.selAnchorRow, e.selAnchorCol)
	}
	if got := e.SelectedText(); got == "" {
		t.Fatal("SelectedText() empty after Shift+Click extend")
	}
}

// Double-click in wrap mode selects the word under the pointer, as it does
// unwrapped.
func TestWrappedMouseDoubleClickSelectsWord(t *testing.T) {
	e := newWrappedTestEditor("hello world again", 11, 4)

	press := func() {
		e.HandleMouse(tcell.NewEventMouse(2, 1, tcell.Button1, tcell.ModNone))
		e.HandleMouse(tcell.NewEventMouse(2, 1, tcell.ButtonNone, tcell.ModNone))
	}
	press()
	press()

	if got, want := e.SelectedText(), "world"; got != want {
		t.Fatalf("SelectedText() = %q, want %q", got, want)
	}
}

// Alt is what arms a block selection on the press, and wrap mode has no block
// selection — so the wrapped press clears the flag instead of setting it,
// whatever the terminal reports.
func TestWrappedMousePressNeverArmsBlockSelection(t *testing.T) {
	e := newWrappedTestEditor("hello world again", 11, 4)
	e.selBlock = true // as if carried in by hand

	e.HandleMouse(tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModAlt))

	if e.selBlock {
		t.Fatal("Alt+press in wrap mode armed a block selection")
	}
}

// The unwrapped press keeps arming one, which is the half applyMousePress must
// not lose in the extraction.
func TestUnwrappedMousePressStillArmsBlockSelection(t *testing.T) {
	e := newTestEditor("hello world again\nsecond line here")
	e.SetGutterVisible(false)
	e.SetBounds(0, 0, 30, 4)

	e.HandleMouse(tcell.NewEventMouse(3, 1, tcell.Button1, tcell.ModAlt))

	if !e.selBlock {
		t.Fatal("Alt+press outside wrap mode did not arm a block selection")
	}
	if e.cursorRow != 1 || e.cursorCol != 3 {
		t.Fatalf("cursor = (%d,%d), want (1,3)", e.cursorRow, e.cursorCol)
	}
}
