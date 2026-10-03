package controls

import "github.com/radix29/gossms/internal/tuikit/sqltext"

// ---------------------------------------------------------------------------
// T-SQL statement-boundary detection for Editor (Ctrl+Enter: select the
// statement at the cursor)
// ---------------------------------------------------------------------------

// SelectStatementAtCursor selects the T-SQL statement containing the cursor,
// by sqltext.StatementAt's rules — the selection is what the next F5 runs.
//
// No-ops (returns false, selection untouched) if the statement at the
// cursor is empty or all-whitespace — e.g. the cursor sits on a blank line
// between two GO separators.
func (e *Editor) SelectStatementAtCursor() bool {
	sr, sc, er, ec, ok := sqltext.StatementAt(e.doc.all(), e.cursorRow, e.cursorCol)
	if !ok {
		return false
	}
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = sr, sc
	e.cursorRow, e.cursorCol = er, ec
	e.clampCursor()
	e.desiredCol = e.cursorDisplayCol()
	e.ensureCursorVisible()
	return true
}
