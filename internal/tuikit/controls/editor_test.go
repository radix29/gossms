package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestEditor(text string) *Editor {
	e := NewEditor(nil)
	e.SetText(text)
	return e
}

func key(k tcell.Key, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, "", mod)
}

func runeKey(r rune, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, string(r), mod)
}

func TestEditorWordNavigation(t *testing.T) {
	e := newTestEditor("foo.bar baz")

	want := []int{3, 4, 7, 11}
	for _, w := range want {
		e.HandleKey(key(tcell.KeyRight, tcell.ModCtrl))
		if e.cursorCol != w {
			t.Fatalf("Ctrl+Right: cursorCol = %d, want %d", e.cursorCol, w)
		}
	}
	wantBack := []int{8, 4, 3, 0}
	for _, w := range wantBack {
		e.HandleKey(key(tcell.KeyLeft, tcell.ModCtrl))
		if e.cursorCol != w {
			t.Fatalf("Ctrl+Left: cursorCol = %d, want %d", e.cursorCol, w)
		}
	}
}

func TestEditorCtrlHomeEnd(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree")
	e.cursorRow, e.cursorCol = 1, 2

	e.HandleKey(key(tcell.KeyEnd, tcell.ModCtrl))
	if e.cursorRow != 2 || e.cursorCol != len("three") {
		t.Fatalf("Ctrl+End: got (%d,%d), want (2,%d)", e.cursorRow, e.cursorCol, len("three"))
	}

	e.HandleKey(key(tcell.KeyHome, tcell.ModCtrl))
	if e.cursorRow != 0 || e.cursorCol != 0 {
		t.Fatalf("Ctrl+Home: got (%d,%d), want (0,0)", e.cursorRow, e.cursorCol)
	}
}

func TestEditorSelectAll(t *testing.T) {
	e := newTestEditor("abc\nde")
	e.HandleKey(key(tcell.KeyCtrlA, tcell.ModNone))
	if !e.HasSelection() {
		t.Fatal("Ctrl+A: expected a selection")
	}
	if got := e.SelectedText(); got != "abc\nde" {
		t.Fatalf("Ctrl+A: SelectedText() = %q, want %q", got, "abc\nde")
	}
}

func TestEditorDuplicateLines(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree")
	e.cursorRow = 1
	e.DuplicateLines()
	if got := e.Text(); got != "one\ntwo\ntwo\nthree" {
		t.Fatalf("DuplicateLines() = %q", got)
	}
	if e.cursorRow != 2 {
		t.Fatalf("cursorRow = %d, want 2", e.cursorRow)
	}
}

func TestEditorDeleteLines(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree")
	e.cursorRow = 1
	e.DeleteLines()
	if got := e.Text(); got != "one\nthree" {
		t.Fatalf("DeleteLines() = %q", got)
	}

	// Deleting the only line preserves the [][]rune{{}} invariant.
	e2 := newTestEditor("only")
	e2.DeleteLines()
	if got := e2.Text(); got != "" {
		t.Fatalf("DeleteLines() on sole line = %q, want empty", got)
	}
	if len(e2.lines) != 1 {
		t.Fatalf("expected the empty-buffer invariant to hold, got %d lines", len(e2.lines))
	}
}

// TestEditorDuplicateDeleteLinesCollapseSelectionWhenCalledDirectly pins
// down that DuplicateLines/DeleteLines collapse an active selection
// themselves, matching their doc comments, rather than relying on
// HandleKey's post-switch dropSelection cleanup: invoked directly (the Edit
// menu's path, see menu.go) they would otherwise leave a stale anchor row,
// which for DeleteLines can point past the new buffer's end and panic on
// the next SelectedText call.
func TestEditorDuplicateDeleteLinesCollapseSelectionWhenCalledDirectly(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree")
	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 2, 3 // selects all three lines

	e.DuplicateLines()
	if e.HasSelection() || e.selBlock {
		t.Fatal("DuplicateLines() called directly should collapse the selection")
	}

	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = len(e.lines)-1, 0 // select down to the last line

	e.DeleteLines()
	if e.HasSelection() || e.selBlock {
		t.Fatal("DeleteLines() called directly should collapse the selection")
	}
	if got := e.SelectedText(); got != "" {
		t.Fatalf("SelectedText() after DeleteLines() = %q, want empty (and must not panic)", got)
	}
}

func TestEditorMoveLines(t *testing.T) {
	e := newTestEditor("one\ntwo\nthree")
	e.cursorRow = 1
	e.MoveLinesUp()
	if got := e.Text(); got != "two\none\nthree" {
		t.Fatalf("MoveLinesUp() = %q", got)
	}
	if e.cursorRow != 0 {
		t.Fatalf("cursorRow = %d, want 0", e.cursorRow)
	}

	// No-op at the top of the buffer.
	e.MoveLinesUp()
	if got := e.Text(); got != "two\none\nthree" {
		t.Fatalf("MoveLinesUp() at top mutated buffer: %q", got)
	}

	e2 := newTestEditor("one\ntwo\nthree")
	e2.cursorRow = 1
	e2.MoveLinesDown()
	if got := e2.Text(); got != "one\nthree\ntwo" {
		t.Fatalf("MoveLinesDown() = %q", got)
	}
	if e2.cursorRow != 2 {
		t.Fatalf("cursorRow = %d, want 2", e2.cursorRow)
	}

	// No-op at the bottom of the buffer.
	e2.MoveLinesDown()
	if got := e2.Text(); got != "one\nthree\ntwo" {
		t.Fatalf("MoveLinesDown() at bottom mutated buffer: %q", got)
	}
}

func TestEditorTabSingleLineUnchanged(t *testing.T) {
	e := newTestEditor("abc")
	e.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if got := e.Text(); got != "    abc" {
		t.Fatalf("Tab with no selection = %q, want %q", got, "    abc")
	}
}

func TestEditorIndentDedentMultiLineSelection(t *testing.T) {
	e := newTestEditor("abc\ndef")
	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 1, 3

	e.HandleKey(key(tcell.KeyTab, tcell.ModNone))
	if got := e.Text(); got != "    abc\n    def" {
		t.Fatalf("Tab (indent) = %q", got)
	}
	if !e.HasSelection() {
		t.Fatal("Tab (indent): expected selection to be preserved")
	}
	if e.selAnchorCol != 4 || e.cursorCol != 7 {
		t.Fatalf("Tab (indent): selAnchorCol=%d cursorCol=%d, want 4,7", e.selAnchorCol, e.cursorCol)
	}

	e.HandleKey(key(tcell.KeyTab, tcell.ModShift))
	if got := e.Text(); got != "abc\ndef" {
		t.Fatalf("Shift+Tab (dedent) = %q", got)
	}
	if e.selAnchorCol != 0 || e.cursorCol != 3 {
		t.Fatalf("Shift+Tab (dedent): selAnchorCol=%d cursorCol=%d, want 0,3", e.selAnchorCol, e.cursorCol)
	}
}

func TestEditorToggleLineComments(t *testing.T) {
	e := newTestEditor("SELECT 1\nSELECT 2")
	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 1, 8

	e.ToggleLineComments()
	if got := e.Text(); got != "-- SELECT 1\n-- SELECT 2" {
		t.Fatalf("comment = %q", got)
	}

	e.ToggleLineComments()
	if got := e.Text(); got != "SELECT 1\nSELECT 2" {
		t.Fatalf("uncomment = %q", got)
	}

	// Mixed state (one line already commented) comments every line,
	// including the already-commented one.
	e.SetText("-- SELECT 1\nSELECT 2")
	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 1, 8
	e.ToggleLineComments()
	if got := e.Text(); got != "-- -- SELECT 1\n-- SELECT 2" {
		t.Fatalf("mixed-state comment = %q", got)
	}
}

func TestEditorCaseConversion(t *testing.T) {
	e := newTestEditor("hello world")
	e.selecting, e.selBlock = true, false
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 0, 5

	e.HandleKey(key(tcell.KeyCtrlU, tcell.ModShift))
	if got := e.Text(); got != "HELLO world" {
		t.Fatalf("uppercase = %q", got)
	}
	if !e.HasSelection() {
		t.Fatal("case conversion: expected selection to be preserved")
	}

	e.HandleKey(key(tcell.KeyCtrlU, tcell.ModNone))
	if got := e.Text(); got != "hello world" {
		t.Fatalf("lowercase = %q", got)
	}
}

// TestEditorSelectionDeletedByBackspace pins down that selecting text then
// pressing Backspace/Delete removes it — dropping the selection before
// deleteSelection() can see it silently leaves the text in place.
func TestEditorSelectionDeletedByBackspace(t *testing.T) {
	e := newTestEditor("abcdef")
	for i := 0; i < 3; i++ {
		e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	if !e.HasSelection() || e.SelectedText() != "abc" {
		t.Fatalf("setup: SelectedText() = %q, want %q", e.SelectedText(), "abc")
	}
	e.HandleKey(key(tcell.KeyBackspace, tcell.ModNone))
	if got := e.Text(); got != "def" {
		t.Fatalf("Backspace over selection = %q, want %q", got, "def")
	}

	e2 := newTestEditor("abcdef")
	for i := 0; i < 3; i++ {
		e2.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	e2.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
	if got := e2.Text(); got != "def" {
		t.Fatalf("Delete over selection = %q, want %q", got, "def")
	}
}

func TestEditorCutCopyPasteRoundTrip(t *testing.T) {
	e := newTestEditor("hello world")
	for i := 0; i < 5; i++ {
		e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	if got := e.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText() = %q, want %q", got, "hello")
	}
	cut := e.Cut()
	if cut != "hello" {
		t.Fatalf("Cut() = %q, want %q", cut, "hello")
	}
	if got := e.Text(); got != " world" {
		t.Fatalf("after Cut(): Text() = %q, want %q", got, " world")
	}
	e.Paste("hello")
	if got := e.Text(); got != "hello world" {
		t.Fatalf("after Paste(): Text() = %q, want %q", got, "hello world")
	}
}

func TestEditorBlockSelection(t *testing.T) {
	e := newTestEditor("abcdef\nxy\nghijkl")
	e.selecting, e.selBlock = true, true
	e.selAnchorRow, e.selAnchorCol = 0, 2
	e.cursorRow, e.cursorCol = 2, 4

	if got := e.SelectedText(); got != "cd\n\nij" {
		t.Fatalf("block SelectedText() = %q, want %q", got, "cd\n\nij")
	}

	cut := e.Cut()
	if cut != "cd\n\nij" {
		t.Fatalf("block Cut() = %q, want %q", cut, "cd\n\nij")
	}
	if got := e.Text(); got != "abef\nxy\nghkl" {
		t.Fatalf("after block Cut(): Text() = %q, want %q", got, "abef\nxy\nghkl")
	}
	if e.HasSelection() || e.selBlock {
		t.Fatal("block Cut() should collapse the selection")
	}
	if e.cursorRow != 0 || e.cursorCol != 2 {
		t.Fatalf("cursor after block Cut() = (%d,%d), want (0,2)", e.cursorRow, e.cursorCol)
	}
}

func TestEditorBlockSelectionDisabledInWrapMode(t *testing.T) {
	e := newTestEditor("hello")
	e.SetWrapMode(true)
	e.HandleKey(key(tcell.KeyRight, tcell.ModAlt|tcell.ModShift))
	if e.HasSelection() || e.selBlock {
		t.Fatal("Alt+Shift+Right should not enter block selection in wrap mode")
	}
}

func TestEditorEscClearsSelection(t *testing.T) {
	e := newTestEditor("abcdef")
	e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	if !e.HasSelection() {
		t.Fatal("setup: expected a selection")
	}
	e.HandleKey(key(tcell.KeyEsc, tcell.ModNone))
	if e.HasSelection() {
		t.Fatal("Esc should clear the selection")
	}
}

func TestEditorCommentToggleShortcut(t *testing.T) {
	// The real legacy path: Ctrl+/ emits the 0x1F byte, which tcell v3
	// decodes as KeyRune '_' with ModCtrl (Ctrl+_), not KeyUS.
	e := newTestEditor("SELECT 1")
	e.HandleKey(runeKey('_', tcell.ModCtrl))
	if got := e.Text(); got != "-- SELECT 1" {
		t.Fatalf("Ctrl+/ (legacy 0x1F -> Ctrl+_) = %q", got)
	}

	// Modern keyboard protocol reports Ctrl+/ as rune '/' with ModCtrl.
	e2 := newTestEditor("SELECT 1")
	e2.HandleKey(runeKey('/', tcell.ModCtrl))
	if got := e2.Text(); got != "-- SELECT 1" {
		t.Fatalf("Ctrl+/ (advanced protocol) = %q", got)
	}

	// Defensive fallback: a terminal that ever surfaced KeyUS directly.
	e3 := newTestEditor("SELECT 1")
	e3.HandleKey(key(tcell.KeyUS, tcell.ModNone))
	if got := e3.Text(); got != "-- SELECT 1" {
		t.Fatalf("Ctrl+/ (KeyUS fallback) = %q", got)
	}
}

func TestEditorRightClickFiresCallback(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetBounds(0, 0, 40, 5)

	// Arm a selection; right-click must leave it untouched so the context
	// menu's Copy/Cut still act on it.
	e.selecting = true
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 0, 5

	var gotX, gotY int
	fired := false
	e.OnRightClick = func(x, y int) { fired, gotX, gotY = true, x, y }

	handled := e.HandleMouse(tcell.NewEventMouse(8, 0, tcell.Button2, tcell.ModNone))
	if !handled {
		t.Fatal("Button2 in the content area should be consumed")
	}
	if !fired {
		t.Fatal("OnRightClick was not called")
	}
	if gotX != 8 || gotY != 0 {
		t.Fatalf("OnRightClick position = (%d,%d), want (8,0)", gotX, gotY)
	}
	if !e.HasSelection() || e.SelectedText() != "hello" {
		t.Fatalf("right-click disturbed the selection: %q", e.SelectedText())
	}
}

// TestEditorCtrlSpaceFiresRightClickAtCursor confirms Ctrl+Space is a
// keyboard equivalent for OnRightClick, positioned at the text cursor
// instead of a mouse click, and — since it's a menu trigger, not a typed
// character — doesn't insert anything or disturb the buffer.
func TestEditorCtrlSpaceFiresRightClickAtCursor(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetBounds(0, 0, 40, 5)
	e.cursorRow, e.cursorCol = 0, 5

	var gotX, gotY int
	fired := false
	e.OnRightClick = func(x, y int) { fired, gotX, gotY = true, x, y }

	if !e.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl)) {
		t.Fatal("Ctrl+Space should be consumed")
	}
	if !fired {
		t.Fatal("OnRightClick was not called")
	}
	// gutterWidth() is 5 by default, so column 5 lands at screen x=10.
	if gotX != 10 || gotY != 0 {
		t.Fatalf("OnRightClick position = (%d,%d), want (10,0)", gotX, gotY)
	}
	if got := e.Text(); got != "hello world" {
		t.Fatalf("Ctrl+Space mutated the buffer: %q", got)
	}
}

// TestEditorSetTextResetsSelectionAndUndo pins down that SetText clears any
// active selection and the undo/redo stacks, which refer to the document
// being replaced: a stale selection anchor past the new (shorter) buffer's
// end makes SelectedText panic, and Undo could otherwise restore text from
// the document that existed before SetText — both reachable via
// connect_dialog.go's fExtraProps.SetText when applying a saved
// connection.
func TestEditorSetTextResetsSelectionAndUndo(t *testing.T) {
	e := newTestEditor("a long original line")
	e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	if !e.HasSelection() {
		t.Fatal("setup: expected a selection")
	}
	e.pushUndo() // simulate prior edit history on the old document

	e.SetText("hi") // shorter than the old selection anchor's column

	if e.HasSelection() {
		t.Fatal("SetText should have cleared the active selection")
	}
	if got := e.SelectedText(); got != "" {
		t.Fatalf("SelectedText() after SetText = %q, want empty (and must not panic)", got)
	}
	if len(e.undoStack) != 0 || len(e.redoStack) != 0 {
		t.Fatalf("SetText should have cleared undo/redo history, got undo=%d redo=%d", len(e.undoStack), len(e.redoStack))
	}
	e.undo() // no-op: must not restore the pre-SetText document
	if got := e.Text(); got != "hi" {
		t.Fatalf("undo() after SetText = %q, want %q (undo history must not cross SetText)", got, "hi")
	}
}

func TestEditorWordDelete(t *testing.T) {
	e := newTestEditor("foo bar baz")
	e.cursorCol = len("foo bar baz")
	e.HandleKey(key(tcell.KeyBackspace, tcell.ModCtrl))
	if got := e.Text(); got != "foo bar " {
		t.Fatalf("Ctrl+Backspace = %q, want %q", got, "foo bar ")
	}

	e2 := newTestEditor("foo bar baz")
	e2.HandleKey(key(tcell.KeyDelete, tcell.ModCtrl))
	if got := e2.Text(); got != " bar baz" {
		t.Fatalf("Ctrl+Delete = %q, want %q", got, " bar baz")
	}
}

// TestEditorUndoStackCapped covers the bounded undo stack: pushUndo drops
// the oldest snapshot once maxUndoSteps is exceeded rather than growing
// without limit. Typing maxUndoSteps+5 characters pushes maxUndoSteps+5
// snapshots, but only the newest maxUndoSteps survive; undoing all of them
// rewinds to 5 characters short of empty.
func TestEditorUndoStackCapped(t *testing.T) {
	e := newTestEditor("")
	for i := 0; i < maxUndoSteps+5; i++ {
		e.HandleKey(runeKey('x', tcell.ModNone))
	}
	if len(e.undoStack) != maxUndoSteps {
		t.Fatalf("undoStack len = %d, want %d (capped)", len(e.undoStack), maxUndoSteps)
	}
	for i := 0; i < maxUndoSteps; i++ {
		e.undo()
	}
	if len(e.undoStack) != 0 {
		t.Fatalf("undoStack should be empty after undoing every retained step, got %d", len(e.undoStack))
	}
	if got := e.Text(); got != "xxxxx" {
		t.Fatalf("Text() after exhausting undo = %q, want %q (the 5 oldest snapshots were evicted, so undo can't reach empty)", got, "xxxxx")
	}
}

// TestEditorShiftClickExtendsFromCursorWhenNoSelection pins down that
// Shift+Click with no active selection yet extends from the cursor's
// current position, rather than re-anchoring at the click point the way a
// plain click does.
func TestEditorShiftClickExtendsFromCursorWhenNoSelection(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetBounds(0, 0, 40, 5)
	e.cursorRow, e.cursorCol = 0, 0

	e.HandleMouse(tcell.NewEventMouse(e.gutterWidth()+5, 0, tcell.Button1, tcell.ModShift))

	if !e.HasSelection() {
		t.Fatal("Shift+Click should have created a selection")
	}
	if got := e.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText() = %q, want %q (cursor's pre-click position to the click)", got, "hello")
	}
}

// TestEditorShiftClickExtendsExistingSelection pins down that Shift+Click
// with a selection already active keeps the existing anchor and only moves
// the cursor, rather than collapsing to a fresh selection anchored at the
// click point.
func TestEditorShiftClickExtendsExistingSelection(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetBounds(0, 0, 40, 5)
	e.cursorRow, e.cursorCol = 0, 0
	for i := 0; i < 3; i++ {
		e.HandleKey(key(tcell.KeyRight, tcell.ModShift))
	}
	if got := e.SelectedText(); got != "hel" {
		t.Fatalf("setup: SelectedText() = %q, want %q", got, "hel")
	}

	e.HandleMouse(tcell.NewEventMouse(e.gutterWidth()+8, 0, tcell.Button1, tcell.ModShift))

	if got := e.SelectedText(); got != "hello wo" {
		t.Fatalf("SelectedText() = %q, want %q (extended from the original anchor, not the click)", got, "hello wo")
	}
}

// TestEditorPlainClickStillReanchors confirms the Shift+Click change left
// plain-click behavior untouched: it still collapses any prior selection
// and re-anchors at the click point.
func TestEditorPlainClickStillReanchors(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetBounds(0, 0, 40, 5)
	e.selecting = true
	e.selAnchorRow, e.selAnchorCol = 0, 0
	e.cursorRow, e.cursorCol = 0, 3

	e.HandleMouse(tcell.NewEventMouse(e.gutterWidth()+6, 0, tcell.Button1, tcell.ModNone))

	if e.HasSelection() {
		t.Fatal("a plain (non-Shift) click should collapse any prior selection, not extend it")
	}
	if e.cursorCol != 6 {
		t.Fatalf("cursorCol after click = %d, want 6", e.cursorCol)
	}
}

// TestEditorShiftClickExtendsSelectionInWrapMode confirms the same
// Shift+Click extension applies to handleMouseWrapped, the wrap-mode
// counterpart of HandleMouse's click handling.
func TestEditorShiftClickExtendsSelectionInWrapMode(t *testing.T) {
	e := newTestEditor("hello world")
	e.SetWrapMode(true)
	e.SetBounds(0, 0, 40, 5)
	e.cursorRow, e.cursorCol = 0, 0

	e.HandleMouse(tcell.NewEventMouse(e.gutterWidth()+5, 0, tcell.Button1, tcell.ModShift))

	if got := e.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText() = %q, want %q", got, "hello")
	}
}

// TestEditorReadOnlyRejectsMutatingKeys confirms SetReadOnly(true) blocks
// every mutating key (typed runes, Enter, Backspace, Tab-indent, Ctrl+D
// duplicate line, undo) without erroring, and that the same keys work
// normally again once read-only is turned back off.
func TestEditorReadOnlyRejectsMutatingKeys(t *testing.T) {
	e := newTestEditor("hello")
	e.SetReadOnly(true)
	e.cursorCol = 5

	mutating := []*tcell.EventKey{
		runeKey('!', tcell.ModNone),
		key(tcell.KeyEnter, tcell.ModNone),
		key(tcell.KeyBackspace, tcell.ModNone),
		key(tcell.KeyDelete, tcell.ModNone),
		key(tcell.KeyTab, tcell.ModNone),
		key(tcell.KeyCtrlD, tcell.ModNone),
		key(tcell.KeyCtrlZ, tcell.ModNone),
	}
	for _, ev := range mutating {
		if e.HandleKey(ev) {
			t.Errorf("HandleKey(%v) returned true while read-only, want false (rejected)", ev.Key())
		}
	}
	if got := e.Text(); got != "hello" {
		t.Fatalf("text mutated while read-only: got %q, want unchanged %q", got, "hello")
	}

	// Movement and selection still work.
	e.cursorCol = 0
	if !e.HandleKey(key(tcell.KeyRight, tcell.ModNone)) || e.cursorCol != 1 {
		t.Error("plain movement should still work while read-only")
	}
	if !e.HandleKey(key(tcell.KeyRight, tcell.ModShift)) || !e.HasSelection() {
		t.Error("Shift+movement (selection) should still work while read-only")
	}
	e.selecting = false
	if !e.HandleKey(key(tcell.KeyCtrlA, tcell.ModNone)) || !e.HasSelection() {
		t.Error("Ctrl+A (Select All) should still work while read-only")
	}

	// Turning read-only back off restores normal editing.
	e.SetReadOnly(false)
	e.selecting = false
	e.cursorRow, e.cursorCol = 0, 5
	e.HandleKey(runeKey('!', tcell.ModNone))
	if got := e.Text(); got != "hello!" {
		t.Fatalf("text after SetReadOnly(false) = %q, want %q", got, "hello!")
	}
}

// TestEditorVerticalMovePreservesDesiredColumn confirms Up/Down use a
// Notepad++/Scintilla-style "goal column": passing through a line too short
// to hold the starting column clamps the cursor for that line only, and the
// original column is restored the moment a long-enough line is reached
// again, rather than staying stuck at the short line's clamped column.
func TestEditorVerticalMovePreservesDesiredColumn(t *testing.T) {
	e := newTestEditor("SELECT column_one\nshort\nFROM some_long_table_name")
	e.cursorRow, e.cursorCol = 0, 10
	e.desiredCol = 10

	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	if e.cursorRow != 1 || e.cursorCol != len("short") {
		t.Fatalf("Down onto a shorter line: got (%d,%d), want (1,%d)", e.cursorRow, e.cursorCol, len("short"))
	}

	e.HandleKey(key(tcell.KeyDown, tcell.ModNone))
	if e.cursorRow != 2 || e.cursorCol != 10 {
		t.Fatalf("Down onto a longer line: got (%d,%d), want (2,10) — the goal column should be restored", e.cursorRow, e.cursorCol)
	}

	e.HandleKey(key(tcell.KeyUp, tcell.ModNone))
	if e.cursorRow != 1 || e.cursorCol != len("short") {
		t.Fatalf("Up back onto the shorter line: got (%d,%d), want (1,%d)", e.cursorRow, e.cursorCol, len("short"))
	}
	e.HandleKey(key(tcell.KeyUp, tcell.ModNone))
	if e.cursorRow != 0 || e.cursorCol != 10 {
		t.Fatalf("Up back onto the original line: got (%d,%d), want (0,10)", e.cursorRow, e.cursorCol)
	}
}

// TestEditorHorizontalMoveResetsDesiredColumn confirms any non-vertical
// cursor movement (Left/Right, typing, a click, …) re-anchors the goal
// column to wherever the cursor actually is, so a later Up/Down aims for
// that new position instead of a stale one left over from an earlier
// vertical run.
func TestEditorHorizontalMoveResetsDesiredColumn(t *testing.T) {
	e := newTestEditor("one two\nshort\nthree four")
	e.cursorRow, e.cursorCol = 0, 7
	e.desiredCol = 7

	e.HandleKey(key(tcell.KeyLeft, tcell.ModNone))
	if e.desiredCol != 6 {
		t.Fatalf("desiredCol after Left = %d, want 6", e.desiredCol)
	}

	e.HandleKey(key(tcell.KeyDown, tcell.ModNone)) // onto "short" (len 5): clamps
	e.HandleKey(key(tcell.KeyDown, tcell.ModNone)) // onto "three four": should land at 6, not the original 7
	if e.cursorRow != 2 || e.cursorCol != 6 {
		t.Fatalf("cursor after two Downs = (%d,%d), want (2,6)", e.cursorRow, e.cursorCol)
	}
}

// TestEditorShiftDownSelectsToEndOfLineThenToSameColumn locks in the exact
// selection shape Shift+Down (or Shift+Up) is expected to produce — the
// same "ragged" multi-line highlight Notepad++/Scintilla renders: from the
// cursor to the end of the starting line, then from the start of the next
// line up to the same column the movement began at.
func TestEditorShiftDownSelectsToEndOfLineThenToSameColumn(t *testing.T) {
	e := newTestEditor("SELECT *\nFROM patients")
	e.cursorRow, e.cursorCol = 0, 3
	e.desiredCol = 3

	e.HandleKey(key(tcell.KeyDown, tcell.ModShift))
	if !e.HasSelection() {
		t.Fatal("Shift+Down should start a selection")
	}
	if e.cursorRow != 1 || e.cursorCol != 3 {
		t.Fatalf("cursor after Shift+Down = (%d,%d), want (1,3)", e.cursorRow, e.cursorCol)
	}

	if start, end, ok := e.selectionRangeForLine(0); !ok || start != 3 || end != len("SELECT *")+1 {
		t.Errorf("line 0 selection range = (%d,%d,%v), want (3,%d,true) — selected from the cursor to end of line",
			start, end, ok, len("SELECT *")+1)
	}
	if start, end, ok := e.selectionRangeForLine(1); !ok || start != 0 || end != 3 {
		t.Errorf("line 1 selection range = (%d,%d,%v), want (0,3,true) — selected from the start of the line to the same column",
			start, end, ok)
	}
}

// TestEditorHorizontalWheelScroll confirms WheelLeft/WheelRight, and
// Shift+WheelUp/WheelDown as the common desktop-convention alias for them,
// all move scrollCol, clamped so the last character of the longest line
// stays reachable — mirrors DataGrid's identical convention
// (TestDataGridHorizontalWheelScroll).
func TestEditorHorizontalWheelScroll(t *testing.T) {
	e := newTestEditor("0123456789abcdefghijklmnopqrstuvwxyz")
	e.SetBounds(0, 0, 10, 5)

	e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelRight, tcell.ModNone))
	if e.scrollCol != horizontalWheelChars {
		t.Fatalf("scrollCol after WheelRight = %d, want %d", e.scrollCol, horizontalWheelChars)
	}
	// Enough further ticks to overshoot the line's end regardless of step size.
	for range len(e.lines[0]) {
		e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelRight, tcell.ModNone))
	}
	want := len(e.lines[0]) - 1
	if e.scrollCol != want {
		t.Fatalf("scrollCol after repeated WheelRight = %d, want %d (clamped to the last character)", e.scrollCol, want)
	}
	e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelLeft, tcell.ModNone))
	if e.scrollCol != want-horizontalWheelChars {
		t.Fatalf("scrollCol after WheelLeft = %d, want %d", e.scrollCol, want-horizontalWheelChars)
	}

	e.scrollCol = 0
	e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelDown, tcell.ModShift))
	if e.scrollCol != horizontalWheelChars {
		t.Fatalf("scrollCol after Shift+WheelDown = %d, want %d", e.scrollCol, horizontalWheelChars)
	}
	e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelUp, tcell.ModShift))
	if e.scrollCol != 0 {
		t.Fatalf("scrollCol after Shift+WheelUp = %d, want 0", e.scrollCol)
	}

	// Plain WheelUp/WheelDown (no Shift) still scroll rows, not columns.
	e.HandleMouse(tcell.NewEventMouse(6, 2, tcell.WheelDown, tcell.ModNone))
	if e.scrollCol != 0 {
		t.Fatalf("plain WheelDown must not touch scrollCol, got %d", e.scrollCol)
	}
}

// ---------------------------------------------------------------------------
// Horizontal scrollbar
// ---------------------------------------------------------------------------

func TestEditorHScrollbarOnlyWhenALineOverflows(t *testing.T) {
	e := newTestEditor("short\nalso short")
	e.SetBounds(0, 0, 40, 6)
	if e.hScrollbarVisible() {
		t.Fatal("bar drawn even though every line fits")
	}
	if got := e.contentH(); got != 6 {
		t.Fatalf("contentH with no bar = %d, want the full height 6", got)
	}

	e.SetText("short\n" + strings.Repeat("x", 200))
	if !e.hScrollbarVisible() {
		t.Fatal("no bar for a buffer whose longest line is far wider than the editor")
	}
	if got := e.contentH(); got != 5 {
		t.Fatalf("contentH with a bar = %d, want 5 — the bottom row belongs to the bar", got)
	}
}

// Word wrap has no horizontal scrolling at all, so it must never give up a
// row to a bar it would then never draw.
func TestEditorHScrollbarNeverInWrapMode(t *testing.T) {
	e := newTestEditor(strings.Repeat("x", 200))
	e.SetBounds(0, 0, 40, 6)
	e.SetWrapMode(true)
	if e.hScrollbarVisible() {
		t.Fatal("bar reported in wrap mode")
	}
	if got := e.contentH(); got != 6 {
		t.Fatalf("contentH in wrap mode = %d, want the full height 6", got)
	}
}

func TestEditorHScrollbarDragScrollsSideways(t *testing.T) {
	e := newTestEditor(strings.Repeat("x", 200))
	e.SetBounds(0, 0, 40, 6)
	x, y, w, _, _, ok := e.hScrollbar()
	if !ok {
		t.Fatal("expected a horizontal scrollbar")
	}

	// Press two-thirds along the track, then release.
	e.HandleMouse(tcell.NewEventMouse(x+w*2/3, y, tcell.Button1, tcell.ModNone))
	if e.scrollCol == 0 {
		t.Fatal("dragging the thumb two-thirds along left scrollCol at 0")
	}
	scrolled := e.scrollCol
	e.HandleMouse(tcell.NewEventMouse(x+w*2/3, y, tcell.ButtonNone, tcell.ModNone))
	if e.sbDraggingX {
		t.Error("sbDraggingX still latched after the release")
	}

	// Back to the far left.
	e.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if e.scrollCol != 0 {
		t.Fatalf("scrollCol after dragging to the far left = %d, want 0 (was %d)", e.scrollCol, scrolled)
	}
	e.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
}

// wideTestEditor is 20 long lines in a 40x6 rect — wide enough and tall
// enough that both scrollbars are showing.
func wideTestEditor() *Editor {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", 200)
	}
	e := newTestEditor(strings.Join(lines, "\n"))
	e.SetBounds(0, 0, 40, 6)
	return e
}

// The bar's row is not a line of text: nothing may put the cursor on the
// line that would otherwise have been drawn there. SetCursorFromScreen is
// the reachable path — the app layer calls it with the drop coordinates of
// an Object Explorer drag-and-drop, which can land anywhere in the rect.
func TestEditorCursorFromScreenSkipsScrollbarRow(t *testing.T) {
	e := wideTestEditor()
	_, y, _, _, _, ok := e.hScrollbar()
	if !ok {
		t.Fatal("expected a horizontal scrollbar")
	}
	if y != 5 {
		t.Fatalf("bar row = %d, want the rect's bottom row 5", y)
	}

	e.SetCursorFromScreen(e.rect.X+e.gutterWidth(), y)
	if e.cursorRow > 4 {
		t.Errorf("cursorRow after dropping on the scrollbar row = %d, want at most 4 — row 5 isn't a text row", e.cursorRow)
	}
}

// A selection dragged downward off the last text line reaches the bar's
// row, and the track spans the whole content width — the gesture has to
// stay with the text, not be taken over by the bar. See the gesture-
// ownership rule in CLAUDE.md.
func TestEditorSelectionDragIsNotStolenByHScrollbar(t *testing.T) {
	e := wideTestEditor()
	_, barY, _, _, _, _ := e.hScrollbar()
	contentX := e.rect.X + e.gutterWidth()

	// Press on the first text line, then drag down across the bar's row.
	e.HandleMouse(tcell.NewEventMouse(contentX+3, 0, tcell.Button1, tcell.ModNone))
	before := e.scrollCol
	e.HandleMouse(tcell.NewEventMouse(contentX+20, barY, tcell.Button1, tcell.ModNone))

	if e.sbDraggingX {
		t.Error("the scrollbar latched onto a text-selection drag that merely crossed its row")
	}
	if e.scrollCol != before {
		t.Errorf("scrollCol moved from %d to %d during a text-selection drag", before, e.scrollCol)
	}
	if !e.selecting {
		t.Error("the text selection was dropped when the drag reached the scrollbar row")
	}
	e.HandleMouse(tcell.NewEventMouse(contentX+20, barY, tcell.ButtonNone, tcell.ModNone))
}

// -- wrap-mode highlighting -------------------------------------------------

// recordingScreen captures what a Draw painted, so a test can assert on the
// style of a given cell rather than only that Draw didn't panic.
type recordingScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]tcell.Style
}

func newRecordingScreen(w, h int) *recordingScreen {
	return &recordingScreen{w: w, h: h, cells: map[[2]int]tcell.Style{}}
}
func (s *recordingScreen) Size() (int, int) { return s.w, s.h }
func (s *recordingScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	s.cells[[2]int{x, y}] = style
}
func (s *recordingScreen) ShowCursor(x, y int) {}

// markerHighlighter colours exactly the rune range [start,end) of every line
// with a style nothing else in the editor uses, so a test can find it.
func markerHighlighter(start, end int, st tcell.Style) Highlighter {
	return func(lines [][]rune, idx int) []ColorRun {
		return []ColorRun{{Start: start, Len: end - start, Style: st}}
	}
}

// TestEditorWrapModeAppliesHighlighter pins that wrap mode and a Highlighter
// compose. drawWrapped originally never called the highlighter at all, so a
// wrapped editor rendered as plain text — silently, since setting both is not
// an error.
func TestEditorWrapModeAppliesHighlighter(t *testing.T) {
	marker := tcell.StyleDefault.Foreground(tcell.ColorFuchsia).Underline(true)

	// Columns 30-34 of the logical line fall on the *second* visual row once
	// the line wraps, which is the case a per-visual-row implementation that
	// forgot to offset by vl.start would get wrong.
	e := NewEditor(markerHighlighter(30, 35, marker))
	e.SetWrapMode(true)
	e.SetGutterVisible(false)
	e.SetText("aaaaaaaaaa bbbbbbbbbb cccccccccc dddddddddd")
	e.SetBounds(0, 0, 22, 6)

	s := newRecordingScreen(40, 10)
	e.Draw(s)

	found := false
	for cell, st := range s.cells {
		if st == marker {
			found = true
			if cell[1] == 0 {
				t.Errorf("highlighted cell at %v is on the first visual row; columns 30-34 wrap onto a later one", cell)
			}
		}
	}
	if !found {
		t.Error("no cell carried the highlighter's style — wrap mode ignored the Highlighter")
	}
}

// TestEditorWrapModeSelectionBeatsHighlighter: selection styling is applied
// after the highlighter in non-wrap mode, and has to stay that way in wrap
// mode, or selecting highlighted text stops looking selected.
func TestEditorWrapModeSelectionBeatsHighlighter(t *testing.T) {
	marker := tcell.StyleDefault.Foreground(tcell.ColorFuchsia).Underline(true)

	e := NewEditor(markerHighlighter(0, 5, marker))
	e.SetWrapMode(true)
	e.SetGutterVisible(false)
	e.SetText("aaaaaaaaaa bbbbbbbbbb")
	e.SetBounds(0, 0, 22, 6)
	e.SelectAll()

	s := newRecordingScreen(40, 10)
	e.Draw(s)

	if st, ok := s.cells[[2]int{0, 0}]; ok && st == marker {
		t.Error("column 0 kept the highlighter style inside a selection; selection must win")
	}
}

func TestStyleAt(t *testing.T) {
	def := tcell.StyleDefault
	a := tcell.StyleDefault.Foreground(tcell.ColorRed)
	b := tcell.StyleDefault.Foreground(tcell.ColorBlue)
	runs := []ColorRun{{Start: 2, Len: 3, Style: a}, {Start: 4, Len: 2, Style: b}}

	cases := []struct {
		ci   int
		want tcell.Style
	}{
		{0, def},
		{2, a},
		{3, a},
		{4, b}, // overlapping runs: the later one wins, as drawHighlighted's map does
		{5, b},
		{6, def},
	}
	for _, tc := range cases {
		if got := styleAt(runs, tc.ci, def); got != tc.want {
			t.Errorf("styleAt(ci=%d) = %v, want %v", tc.ci, got, tc.want)
		}
	}
}
