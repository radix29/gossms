package controls

import (
	"slices"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Editor undo/redo
// ---------------------------------------------------------------------------

// editorState is one undo/redo step: the lines that occupied rows
// [row, row+len(old)) before an edit, how many rows occupy that same place
// after it, and where the cursor was beforehand. bytes is the approximate
// heap cost of old, computed once when the step is built so trimUndo never
// has to walk it again.
//
// A step is a delta rather than a whole-document snapshot because pushUndo
// runs on every keystroke: copying the buffer cost 3.2 ms and 5 MB per
// character in a 20,000-line script, all of it to record a one-line change.
//
// newLen is not knowable when the step is pushed — the edit hasn't run yet —
// so the newest step is left open and closed by finalizeStep. docLen is the
// document's line count at push time, which is what lets it be closed: an
// edit confined to the span can only have changed the count from inside it,
// so the span's new extent is its old one plus the document's net growth.
//
// A closed step's [row, row+newLen) must lie inside the document it will be
// applied to — applyStep slices by it unguarded. That holds only as long as
// every pushUndoSpan caller keeps its promise, so a violated span shows up
// here as an out-of-range panic rather than wherever the promise was broken;
// TestEditorUndoRestoresEveryEditPath checks the invariant per edit path for
// that reason.
type editorState struct {
	row    int
	old    [][]rune
	newLen int
	docLen int

	cursorRow int
	cursorCol int
	bytes     int
}

// cloneLines deep-copies lines so an undo step never aliases the document.
// Several edit paths rewrite a line's runes in place — transformSelection
// does it deliberately, and backspace's append(line[:c-1], line[c:]...) does
// it incidentally — which would otherwise scribble over the very step meant
// to undo them.
func cloneLines(lines [][]rune) [][]rune {
	out := make([][]rune, len(lines))
	for i, l := range lines {
		out[i] = slices.Clone(l)
	}
	return out
}

// linesBytes is the approximate heap cost of lines.
func linesBytes(lines [][]rune) int {
	n := 0
	for _, l := range lines {
		n += len(l)*4 + sliceHeaderBytes
	}
	return n
}

// maxUndoSteps caps the undo stack so unbounded editing doesn't grow it
// forever. Oldest steps are dropped first.
//
// The redo stack has no cap of its own, and is bounded only in *count*: undo
// pushes one entry per pop, so redo can never hold more entries than the undo
// stack did, and a new edit clears it outright.
//
// It is deliberately not bounded in bytes, and maxUndoBytes does not reach it.
// applyStep's inverse carries the lines being *replaced* — the document as it
// is now — not the lines the popped step carried, so on a document that grew
// over its history each inverse is larger than the step that produced it and
// the redo total ends up above the undo total: 48.4 MB of redo against 46.5 MB
// of undo, measured. What keeps that from running away is the undo stack's own
// byte cap, which bounds how much history there is to walk back through; the
// measured worst case is one document's worth over maxUndoBytes, not a
// multiple of it. Adding a redoBytes cap would buy that one document back in
// exchange for silently dropping the deepest redo, so it hasn't been done.
const maxUndoSteps = 500

// maxUndoBytes caps the undo stack's total size, because a step count alone
// does not bound memory: a step covering the whole document (Paste, Replace
// All, the line actions) copies the whole buffer, so 500 of them over a
// 20,000-line script is ~2.5 GB. Whichever cap binds first wins, and the
// newest step is always kept even if it alone exceeds this — one undo has to
// work on any document.
const maxUndoBytes = 64 << 20

// sliceHeaderBytes is what one line's []rune header costs on a 64-bit build,
// counted per line so a document of many short lines is not measured as
// nearly free.
const sliceHeaderBytes = 24

// pushUndo records an undo step covering the whole document — the
// conservative form, for an edit whose reach isn't confined to a row range
// the caller knows in advance (Paste, Cut, Replace All, the line and block
// actions). Per-keystroke paths use pushUndoLocal, which is the whole point
// of steps being deltas.
func (e *Editor) pushUndo() { e.pushUndoSpan(0, e.doc.Len()) }

// pushUndoLocal records an undo step covering only the rows a keystroke-sized
// edit can reach — see editSpan.
func (e *Editor) pushUndoLocal() { e.pushUndoSpan(e.editSpan()) }

// pushUndoSpan records an undo step for an edit confined to rows [lo, hi).
//
// The caller promises to modify no line outside that range, and to make any
// line-count change from inside it. A span narrower than the edit it covers
// does not fail loudly — undo restores a document that was never typed — so
// every call site is round-tripped by TestEditorUndoRestoresEveryEditPath.
func (e *Editor) pushUndoSpan(lo, hi int) {
	e.finalizeStep()
	st := editorState{
		row:       lo,
		old:       cloneLines(e.doc.all()[lo:hi]),
		docLen:    e.doc.Len(),
		cursorRow: e.cursorRow,
		cursorCol: e.cursorCol,
	}
	st.bytes = linesBytes(st.old)
	e.undoStack = append(e.undoStack, st)
	e.undoBytes += st.bytes
	e.stepOpen = true
	e.trimUndo()
	e.redoStack = nil
}

// editSpan is the row range a keystroke-sized edit can touch: the rows the
// selection covers, or the cursor's own row, widened by one either side
// because Backspace at column 0 joins onto the line above and Delete at end
// of line pulls the one below up.
//
// Widening costs two extra line copies per keystroke. Not widening enough is
// what corrupts a document on undo, so the two rows are cheap insurance.
func (e *Editor) editSpan() (lo, hi int) {
	lo, hi = e.cursorRow, e.cursorRow+1
	if e.HasSelection() {
		sr, _, er, _ := e.selectionBounds()
		lo, hi = min(lo, sr), max(hi, er+1)
	}
	return core.Clamp(lo-1, 0, e.doc.Len()), core.Clamp(hi+1, 0, e.doc.Len())
}

// finalizeStep closes the newest undo step. A step is pushed before its edit
// runs, so it cannot know how many rows ended up in the span it covers; the
// edit is confined to that span, so the span's new extent is its old one plus
// however much the whole document grew or shrank. Called by everything that
// reads or replaces the stack.
func (e *Editor) finalizeStep() {
	if !e.stepOpen {
		return
	}
	e.stepOpen = false
	st := &e.undoStack[len(e.undoStack)-1]
	st.newLen = len(st.old) + e.doc.Len() - st.docLen
}

// trimUndo drops the oldest steps until the stack is inside both caps,
// keeping at least one step.
func (e *Editor) trimUndo() {
	drop := 0
	for len(e.undoStack)-drop > 1 &&
		(len(e.undoStack)-drop > maxUndoSteps || e.undoBytes > maxUndoBytes) {
		e.undoBytes -= e.undoStack[drop].bytes
		drop++
	}
	if drop == 0 {
		return
	}
	// Shifted down in place, then the vacated tail cleared. Reslicing to
	// e.undoStack[drop:] instead would keep every dropped step's lines
	// reachable behind the slice header for as long as the editor lives,
	// which is the whole point of dropping them; copying into a fresh array
	// avoided that but left cap == len, so the next push reallocated the whole
	// stack — 38 KB per keystroke once the step cap binds.
	kept := e.undoStack[:copy(e.undoStack, e.undoStack[drop:])]
	clear(e.undoStack[len(kept):])
	e.undoStack = kept
}

// applyStep puts st's saved lines back where they came from and returns the
// closed step that reverses it — so undo and redo are the same operation, and
// a step pushed onto one stack by popping the other is always closed.
//
// The document takes ownership of st.old rather than a copy of it, which is
// sound only because every caller has already popped st and never applies a
// step twice: the reverse step is built from the lines being replaced, not
// from st.
//
// The slice below is deliberately unguarded — see editorState.newLen for the
// invariant. Clamping it instead would convert a broken span promise, which
// is a document-corrupting bug, into an undo that quietly restores the wrong
// text; the panic is the more useful failure.
func (e *Editor) applyStep(st editorState) editorState {
	inv := editorState{
		row:       st.row,
		old:       cloneLines(e.doc.all()[st.row : st.row+st.newLen]),
		newLen:    len(st.old),
		docLen:    e.doc.Len(),
		cursorRow: e.cursorRow,
		cursorCol: e.cursorCol,
	}
	inv.bytes = linesBytes(inv.old)
	e.doc.replaceRange(st.row, st.newLen, st.old)
	e.cursorRow, e.cursorCol = st.cursorRow, st.cursorCol
	e.clampCursor()
	return inv
}

func (e *Editor) undo() {
	e.finalizeStep()
	if len(e.undoStack) == 0 {
		return
	}
	st := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.undoBytes -= st.bytes
	e.redoStack = append(e.redoStack, e.applyStep(st))
}

func (e *Editor) redo() {
	e.finalizeStep()
	if len(e.redoStack) == 0 {
		return
	}
	st := e.redoStack[len(e.redoStack)-1]
	e.redoStack = e.redoStack[:len(e.redoStack)-1]
	inv := e.applyStep(st)
	e.undoStack = append(e.undoStack, inv)
	e.undoBytes += inv.bytes
	e.trimUndo()
}
