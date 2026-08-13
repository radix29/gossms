package controls

import (
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Find and replace for Editor
// ---------------------------------------------------------------------------

// SearchOptions describes one find/replace request. Literal and regular
// expression searches share a single engine: a literal Query is escaped with
// regexp.QuoteMeta and compiled the same way, so match iteration, whole-word
// handling and case folding have exactly one implementation.
type SearchOptions struct {
	Query     string
	Replace   string
	MatchCase bool
	WholeWord bool
	Regexp    bool

	// InSelection restricts ReplaceAll to the selection that was active when
	// SetSearch ran. It has no effect on FindNext, which always searches the
	// whole document — a "find next" that stopped at a selection boundary
	// would be indistinguishable from "no more matches".
	InSelection bool
}

// searchMatch is one match, as rune indices into a single logical line.
// Matches never span lines: the pattern is applied per line, so a regexp
// can't match across a line break. That also keeps the per-line match list
// directly usable by the drawing path.
type searchMatch struct {
	row      int
	startCol int
	endCol   int
}

// editorSearch is Editor's find/replace state: the compiled pattern, the
// match list derived from it, and which match is current.
//
// matches is cached against the document version the scan ran on. Draw
// consults it on every event the app processes, so rescanning per Draw would
// be an O(document) regexp sweep per keystroke — the same cost the Document
// version counter exists to remove for the highlighters.
type editorSearch struct {
	opts SearchOptions
	re   *regexp.Regexp

	matches    []searchMatch
	cur        int // index into matches, or -1 when nothing is current
	scanned    bool
	scanVer    uint64
	scanDocPtr *Document

	// selStart/selEnd bound an InSelection ReplaceAll. Captured at SetSearch
	// time because replacing text moves the selection, so the second
	// replacement onward would otherwise be measured against a range the
	// first one already invalidated.
	selValid                 bool
	selStartRow, selStartCol int
	selEndRow, selEndCol     int
}

// SetSearch compiles opts into the editor's active search and drops any
// previous match state. An invalid regular expression is reported as an
// error and leaves the editor with no active search, so a half-typed
// pattern highlights nothing rather than the previous pattern's hits.
//
// An empty Query clears the search, same as ClearSearch.
func (e *Editor) SetSearch(opts SearchOptions) error {
	if opts.Query == "" {
		e.ClearSearch()
		return nil
	}
	pat := opts.Query
	if !opts.Regexp {
		pat = regexp.QuoteMeta(pat)
	}
	if opts.WholeWord {
		pat = `\b(?:` + pat + `)\b`
	}
	if !opts.MatchCase {
		pat = `(?i)` + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		e.ClearSearch()
		return err
	}
	e.search = editorSearch{opts: opts, re: re, cur: -1}
	if opts.InSelection && e.HasSelection() && !e.selBlock {
		sr, sc, er, ec := e.selectionBounds()
		e.search.selValid = true
		e.search.selStartRow, e.search.selStartCol = sr, sc
		e.search.selEndRow, e.search.selEndCol = er, ec
	}
	return nil
}

// ClearSearch drops the active search, its match list, and the highlighting
// that goes with it.
func (e *Editor) ClearSearch() { e.search = editorSearch{cur: -1} }

// HasSearch reports whether a search pattern is active.
func (e *Editor) HasSearch() bool { return e.search.re != nil }

// SearchOpts returns the options the active search was compiled from.
func (e *Editor) SearchOpts() SearchOptions { return e.search.opts }

// scanMatches rebuilds the match list if it isn't current for this document
// version, and returns it.
func (e *Editor) scanMatches() []searchMatch {
	s := &e.search
	if s.re == nil {
		return nil
	}
	if s.scanned && s.scanDocPtr == e.doc && s.scanVer == e.doc.Version() {
		return s.matches
	}
	s.matches = s.matches[:0]
	for row, line := range e.doc.all() {
		text := string(line)
		if text == "" {
			continue
		}
		// Byte offsets from the regexp engine are converted to rune indices
		// once per line rather than per match: every position Editor works
		// in — cursorCol, selection bounds, ColorRun.Start — is a rune index,
		// and a byte offset that reaches any of them lands mid-character on
		// the first non-ASCII line.
		byteToRune := byteRuneIndex(text)
		for _, loc := range s.re.FindAllStringIndex(text, -1) {
			start, end := loc[0], loc[1]
			if byteToRune != nil {
				start, end = byteToRune[start], byteToRune[end]
			}
			if start == end {
				// A zero-width match (`^`, `\b`, `x*`) has nothing to select
				// or replace, and stepping onto one would make Find Next
				// stall on it forever.
				continue
			}
			s.matches = append(s.matches, searchMatch{row: row, startCol: start, endCol: end})
		}
	}
	s.scanned, s.scanVer, s.scanDocPtr = true, e.doc.Version(), e.doc
	return s.matches
}

// byteRuneIndex maps every byte offset of s that starts a rune (plus len(s))
// to that rune's index. Offsets inside a multi-byte rune are left at zero;
// regexp match bounds always land on rune boundaries, so those are never
// read.
//
// Returns nil when s is pure ASCII, where the mapping is the identity and the
// caller uses the byte offset as the rune index. That is the overwhelmingly
// common case in T-SQL, and building the map cost one len(s)+1 allocation per
// line on every rescan — one per line of the whole document, since an edit
// invalidates the scan.
func byteRuneIndex(s string) []int {
	if !hasMultiByte(s) {
		return nil
	}
	idx := make([]int, len(s)+1)
	n := 0
	for i := range s {
		idx[i] = n
		n++
	}
	idx[len(s)] = n
	return idx
}

// hasMultiByte reports whether s holds any byte outside ASCII, i.e. whether
// byte offsets and rune indices can differ within it.
func hasMultiByte(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// MatchCount returns the number of matches in the document for the active
// search.
func (e *Editor) MatchCount() int { return len(e.scanMatches()) }

// MatchPosition returns the 1-based ordinal of the current match and the
// total match count, for a "Match 2 of 7" readout. i is 0 when no match is
// current — nothing has been found yet, or an edit invalidated the last one.
func (e *Editor) MatchPosition() (i, n int) {
	matches := e.scanMatches()
	if e.search.cur < 0 || e.search.cur >= len(matches) {
		return 0, len(matches)
	}
	return e.search.cur + 1, len(matches)
}

// WordAtCursor returns the identifier the caret sits in or next to, or ""
// when it's on whitespace or punctuation. Used by "find the word under the
// cursor" (Ctrl+F3), which needs the word without disturbing the selection.
func (e *Editor) WordAtCursor() string {
	line := e.doc.Line(e.cursorRow)
	if len(line) == 0 {
		return ""
	}
	col := core.Clamp(e.cursorCol, 0, len(line))
	// Prefer the word to the left when the caret sits just past its last
	// rune, which is where it lands after double-click or word-jump.
	probe := col
	if probe >= len(line) || !core.IsWordRune(line[probe]) {
		if probe > 0 && core.IsWordRune(line[probe-1]) {
			probe--
		} else {
			return ""
		}
	}
	start := probe
	for start > 0 && core.IsWordRune(line[start-1]) {
		start--
	}
	end := probe
	for end < len(line) && core.IsWordRune(line[end]) {
		end++
	}
	return string(line[start:end])
}

// CurrentMatchPos returns the 1-based line and column the current match
// starts at, for a status readout. ok is false when no match is current.
func (e *Editor) CurrentMatchPos() (line, col int, ok bool) {
	matches := e.scanMatches()
	if e.search.cur < 0 || e.search.cur >= len(matches) {
		return 0, 0, false
	}
	m := matches[e.search.cur]
	return m.row + 1, m.startCol + 1, true
}

// FindNext moves to the next match after the cursor (dir >= 0) or the last
// one before it (dir < 0), selects it, and scrolls it into view. It wraps
// around the end of the document, and reports whether a match was found at
// all — false means the document contains none.
//
// The search always starts from the cursor rather than from the previous
// match's index, so a Find Next after the user has clicked elsewhere
// continues from where they are looking, matching every other editor.
func (e *Editor) FindNext(dir int) bool {
	matches := e.scanMatches()
	if len(matches) == 0 {
		e.search.cur = -1
		return false
	}
	idx := -1
	if dir >= 0 {
		// Strictly after the cursor's column, so repeated Find Next steps off
		// the match it just selected (whose start is at the cursor) instead
		// of re-selecting it.
		row, col := e.cursorRow, e.cursorCol
		if e.HasSelection() {
			// A selection's start is where the current match begins; search
			// from its end so the match under the selection is skipped.
			_, _, er, ec := e.selectionBounds()
			row, col = er, ec
		}
		for i, m := range matches {
			if m.row > row || (m.row == row && m.startCol >= col) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0 // wrap to the top
		}
	} else {
		row, col := e.cursorRow, e.cursorCol
		if e.HasSelection() {
			// Step back from the selection's *start*, so a Find Previous on
			// an already-selected match moves off it rather than finding it
			// again.
			row, col, _, _ = e.selectionBounds()
		}
		for i := len(matches) - 1; i >= 0; i-- {
			m := matches[i]
			if m.row < row || (m.row == row && m.endCol <= col) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = len(matches) - 1 // wrap to the bottom
		}
	}
	e.search.cur = idx
	e.selectMatch(matches[idx])
	return true
}

// selectMatch makes m the editor's selection, with the cursor at its end,
// and scrolls it into view.
func (e *Editor) selectMatch(m searchMatch) {
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = m.row, m.startCol
	e.cursorRow, e.cursorCol = m.row, m.endCol
	e.clampCursor()
	e.desiredCol = e.cursorDisplayCol()
	e.ensureCursorVisible()
	// A match far to the right of a long line is inside the viewport
	// vertically but off it horizontally; ensureCursorVisible only scrolls
	// the row into view in plain mode when the column is already visible.
	e.ensureColumnVisible()
}

// ReplaceCurrent replaces the selected match with the active search's
// Replace text and advances to the next match, reporting whether it
// replaced anything. It does nothing unless the selection is exactly a
// match — the SSMS/VS rule that Replace on a fresh dialog finds first and
// replaces on the second press, rather than overwriting whatever happened
// to be selected.
//
// For a regexp search the replacement goes through Regexp.ReplaceAllString,
// so $1 group references work; for a literal search it is inserted as-is.
func (e *Editor) ReplaceCurrent() bool {
	if e.readOnly || e.search.re == nil {
		return false
	}
	matches := e.scanMatches()
	if e.search.cur < 0 || e.search.cur >= len(matches) {
		return false
	}
	m := matches[e.search.cur]
	if !e.selectionIsMatch(m) {
		return false
	}
	// replaceMatch rewrites one line and never changes the line count, so the
	// step is that one row — Replace/F3 down a large script is a held key.
	e.pushUndoSpan(m.row, m.row+1)
	e.replaceMatch(m)
	e.search.cur = -1
	e.FindNext(1)
	return true
}

// selectionIsMatch reports whether the current selection covers exactly m.
func (e *Editor) selectionIsMatch(m searchMatch) bool {
	if !e.HasSelection() || e.selBlock {
		return false
	}
	sr, sc, er, ec := e.selectionBounds()
	return sr == m.row && er == m.row && sc == m.startCol && ec == m.endCol
}

// replaceMatch substitutes m's text in place. The caller owns the undo step:
// ReplaceAll pushes exactly one for the whole run, so this must not push its
// own or a Replace All would take one Ctrl+Z per occurrence to undo.
func (e *Editor) replaceMatch(m searchMatch) {
	line := e.doc.Line(m.row)
	old := string(line[m.startCol:m.endCol])
	repl := e.search.opts.Replace
	if e.search.opts.Regexp {
		repl = e.search.re.ReplaceAllString(old, repl)
	}
	replRunes := []rune(expandTabs(repl))

	updated := make([]rune, 0, len(line)-(m.endCol-m.startCol)+len(replRunes))
	updated = append(updated, line[:m.startCol]...)
	updated = append(updated, replRunes...)
	updated = append(updated, line[m.endCol:]...)
	e.doc.setLine(m.row, updated)

	e.selecting = false
	e.cursorRow, e.cursorCol = m.row, m.startCol+len(replRunes)
	e.clampCursor()
}

// ReplaceAll replaces every match in the document — or, when the search was
// compiled with InSelection and a selection was active at that point, every
// match inside that selection — and returns how many it replaced.
//
// The whole run is one undo step, and each line is rewritten right-to-left
// so that replacing one match doesn't shift the rune offsets of the matches
// still to be replaced on the same line.
func (e *Editor) ReplaceAll() int {
	if e.readOnly || e.search.re == nil {
		return 0
	}
	matches := e.scanMatches()
	targets := make([]searchMatch, 0, len(matches))
	for _, m := range matches {
		if e.matchInScope(m) {
			targets = append(targets, m)
		}
	}
	if len(targets) == 0 {
		return 0
	}
	e.pushUndo()
	for i := len(targets) - 1; i >= 0; i-- {
		e.replaceMatch(targets[i])
	}
	e.search.cur = -1
	e.selecting = false
	e.clampCursor()
	e.ensureCursorVisible()
	return len(targets)
}

// matchInScope reports whether m falls within an InSelection run's captured
// range. Always true when the search isn't scoped to a selection.
func (e *Editor) matchInScope(m searchMatch) bool {
	s := &e.search
	if !s.opts.InSelection {
		return true
	}
	if !s.selValid {
		// InSelection was asked for with nothing selected: replacing the
		// whole document instead would be the opposite of what was asked.
		return false
	}
	if m.row < s.selStartRow || m.row > s.selEndRow {
		return false
	}
	if m.row == s.selStartRow && m.startCol < s.selStartCol {
		return false
	}
	if m.row == s.selEndRow && m.endCol > s.selEndCol {
		return false
	}
	return true
}

// matchSpansForLine returns every match on row. Used by the drawing path,
// once per drawn row: the match list is sorted by row, so the row's slice is
// found by binary search rather than by walking a list that can hold a hit
// for every line in the buffer. The result aliases the cached list and must
// not be retained or modified.
//
// The current match is included like any other. It is also the editor's
// selection, and the selection style wins in styleForRune, so it stays
// visually distinct without being special-cased here.
func (e *Editor) matchSpansForLine(row int) []searchMatch {
	matches := e.scanMatches()
	if len(matches) == 0 {
		return nil
	}
	start, _ := slices.BinarySearchFunc(matches, row, func(m searchMatch, r int) int {
		return m.row - r
	})
	end := start
	for end < len(matches) && matches[end].row == row {
		end++
	}
	if start == end {
		return nil
	}
	return matches[start:end]
}

// ensureColumnVisible scrolls horizontally so the cursor's display column is
// inside the content area. ensureCursorVisible handles the vertical axis
// only; without this a match found far along a wide line selects and scrolls
// vertically to a row whose matched text is still off the right edge.
func (e *Editor) ensureColumnVisible() {
	if e.wrapMode {
		return
	}
	contentW := e.rect.W - e.gutterWidth()
	if contentW <= 0 {
		return
	}
	col := e.cursorDisplayCol()
	if col < e.scrollCol {
		e.scrollCol = col
	} else if col >= e.scrollCol+contentW {
		e.scrollCol = col - contentW + 1
	}
	e.scrollCol = max(0, e.scrollCol)
}
