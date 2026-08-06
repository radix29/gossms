package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// blockSelect arms a block (column) selection over rows [topRow, botRow] at
// columns [anchorCol, cursorCol), the state Alt+Shift+Arrow or an Alt+drag
// leaves behind.
func blockSelect(e *Editor, topRow, anchorCol, botRow, cursorCol int) {
	e.selecting, e.selBlock = true, true
	e.selAnchorRow, e.selAnchorCol = topRow, anchorCol
	e.cursorRow, e.cursorCol = botRow, cursorCol
}

func TestEditorBlockTypingInsertsOnEveryRow(t *testing.T) {
	e := newTestEditor("aaaa\nbbbb\ncccc")
	blockSelect(e, 0, 2, 2, 2) // zero-width column caret at column 2

	e.HandleKey(runeKey('X', tcell.ModNone))
	e.HandleKey(runeKey('Y', tcell.ModNone))

	if got, want := e.Text(), "aaXYaa\nbbXYbb\nccXYcc"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	if !e.blockEditing() {
		t.Fatal("block should stay armed after typing, so the next key keeps going into every row")
	}
	if e.selAnchorCol != 4 || e.cursorCol != 4 {
		t.Fatalf("block column after typing = (%d,%d), want (4,4)", e.selAnchorCol, e.cursorCol)
	}
}

func TestEditorBlockTypingReplacesSelectedColumns(t *testing.T) {
	e := newTestEditor("abcdef\nghijkl\nmnopqr")
	blockSelect(e, 0, 1, 2, 4)

	e.HandleKey(runeKey('_', tcell.ModNone))

	if got, want := e.Text(), "a_ef\ng_kl\nm_qr"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

// A row shorter than the block's column is padded with spaces so the inserted
// text stays in the column the user aimed at, matching SSMS/Notepad++.
func TestEditorBlockTypingPadsShortRows(t *testing.T) {
	e := newTestEditor("aaaa\nb\ncccc")
	blockSelect(e, 0, 3, 2, 3)

	e.HandleKey(runeKey('X', tcell.ModNone))

	if got, want := e.Text(), "aaaXa\nb  X\ncccXc"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestEditorBlockBackspaceAndDelete(t *testing.T) {
	e := newTestEditor("aaaa\nbb\ncccc")
	blockSelect(e, 0, 3, 2, 3)

	e.HandleKey(key(tcell.KeyBackspace, tcell.ModNone))
	// Row 1 has no rune at column 2, so it is left alone rather than padded.
	if got, want := e.Text(), "aaa\nbb\nccc"; got != want {
		t.Fatalf("after Backspace: Text() = %q, want %q", got, want)
	}
	if e.selAnchorCol != 2 || e.cursorCol != 2 {
		t.Fatalf("block column after Backspace = (%d,%d), want (2,2)", e.selAnchorCol, e.cursorCol)
	}

	e.HandleKey(key(tcell.KeyDelete, tcell.ModNone))
	if got, want := e.Text(), "aa\nbb\ncc"; got != want {
		t.Fatalf("after Delete: Text() = %q, want %q", got, want)
	}
}

func TestEditorBlockBackspaceDeletesSelectedColumns(t *testing.T) {
	e := newTestEditor("abcdef\nghijkl")
	blockSelect(e, 0, 1, 1, 3)

	e.HandleKey(key(tcell.KeyBackspace, tcell.ModNone))

	if got, want := e.Text(), "adef\ngjkl"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestEditorBlockTabIndentsEveryRow(t *testing.T) {
	e := newTestEditor("aa\nbb")
	blockSelect(e, 0, 1, 1, 1)

	e.HandleKey(key(tcell.KeyTab, tcell.ModNone))

	if got, want := e.Text(), "a    a\nb    b"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

// A zero-width block is a column caret, not a selection: copying it must not
// produce a run of newlines (which would then execute as a blank query).
func TestEditorZeroWidthBlockCopiesNothing(t *testing.T) {
	e := newTestEditor("aaaa\nbbbb")
	blockSelect(e, 0, 2, 1, 2)

	if got := e.SelectedText(); got != "" {
		t.Fatalf("zero-width block SelectedText() = %q, want %q", got, "")
	}
}

// Text copied out of a block selection goes back in as a block — one line per
// row at the cursor's column — even though the OS clipboard carries no mark
// saying so.
func TestEditorBlockCopyPastesBackAsBlock(t *testing.T) {
	e := newTestEditor("12ab\n34cd\n56ef")
	blockSelect(e, 0, 2, 2, 4)

	copied := e.SelectedText()
	if copied != "ab\ncd\nef" {
		t.Fatalf("block SelectedText() = %q, want %q", copied, "ab\ncd\nef")
	}

	e.selecting, e.selBlock = false, false
	e.cursorRow, e.cursorCol = 0, 0
	e.Paste(copied)

	if got, want := e.Text(), "ab12ab\ncd34cd\nef56ef"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	if e.cursorRow != 2 || e.cursorCol != 2 {
		t.Fatalf("cursor after block paste = (%d,%d), want (2,2)", e.cursorRow, e.cursorCol)
	}
}

// A block paste taller than the remaining buffer appends the rows it needs,
// padded to the paste column so the block stays rectangular.
func TestEditorBlockPasteExtendsBuffer(t *testing.T) {
	e := newTestEditor("12ab\n34cd\n56ef")
	blockSelect(e, 0, 2, 2, 4)
	copied := e.SelectedText()

	e.selecting, e.selBlock = false, false
	e.cursorRow, e.cursorCol = 2, 4
	e.Paste(copied)

	if got, want := e.Text(), "12ab\n34cd\n56efab\n    cd\n    ef"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

// Text that didn't come from a block selection still pastes as a plain
// stream, even while a block selection is showing.
func TestEditorNonBlockPasteStaysLinear(t *testing.T) {
	e := newTestEditor("12ab\n34cd")
	e.cursorRow, e.cursorCol = 0, 0
	e.Paste("xy\nz")

	if got, want := e.Text(), "xy\nz12ab\n34cd"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestEditorBlockPasteReplacesBlockSelection(t *testing.T) {
	e := newTestEditor("12ab\n34cd\n56ef")
	blockSelect(e, 0, 2, 2, 4)
	copied := e.SelectedText()

	blockSelect(e, 0, 0, 2, 2)
	e.Paste(copied)

	if got, want := e.Text(), "abab\ncdcd\nefef"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestEditorBlockEditIsOneUndoStep(t *testing.T) {
	e := newTestEditor("aaaa\nbbbb")
	blockSelect(e, 0, 2, 1, 2)
	e.HandleKey(runeKey('X', tcell.ModNone))

	e.HandleKey(key(tcell.KeyCtrlZ, tcell.ModNone))
	if got, want := e.Text(), "aaaa\nbbbb"; got != want {
		t.Fatalf("after undo: Text() = %q, want %q", got, want)
	}
}
