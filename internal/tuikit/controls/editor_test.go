package controls

import (
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

// TestEditorDuplicateDeleteLinesCollapseSelectionWhenCalledDirectly is a
// regression test: DuplicateLines/DeleteLines must collapse an active
// selection themselves, matching their doc comments. Before the fix, only
// HandleKey's post-switch dropSelection cleanup did this — invoking them
// directly (the Edit menu's path, see menu.go) left a selection with a
// stale anchor row, which for DeleteLines could point past the new
// buffer's end and panic on the next SelectedText call.
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

// TestEditorSelectionDeletedByBackspace is a regression test: selecting
// text then pressing Backspace/Delete must actually remove it. Before the
// HandleKey restructure, the pre-switch "drop selection" assignment ran
// before deleteSelection() could see it, silently leaving the selected
// text in place.
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

// TestEditorSetTextResetsSelectionAndUndo is a regression test: SetText
// must clear any active selection and the undo/redo stacks, which refer to
// the document being replaced. Before the fix, a stale selection anchor
// left past the new (shorter) buffer's end made SelectedText panic, and
// Undo could restore text from the document that existed before SetText —
// both reachable via connect_dialog.go's fExtraProps.SetText when applying
// a saved connection.
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

// TestEditorUndoStackCapped is a regression test for the bounded undo
// stack: pushUndo must drop the oldest snapshot once maxUndoSteps is
// exceeded, rather than growing without limit. Typing maxUndoSteps+5
// characters pushes maxUndoSteps+5 snapshots, but only the newest
// maxUndoSteps survive; undoing all of them can only rewind 5 characters
// short of empty.
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

// TestEditorShiftClickExtendsFromCursorWhenNoSelection is a regression
// test: Shift+Click on a fresh click (no active selection yet) must
// extend from the cursor's current position, not re-anchor at the click
// point the way a plain click does.
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

// TestEditorShiftClickExtendsExistingSelection is a regression test:
// Shift+Click with a selection already active must keep the existing
// anchor and only move the cursor, rather than collapsing to a fresh
// selection anchored at the click point.
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
