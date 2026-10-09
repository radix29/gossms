package controls

import (
	"regexp"
	"regexp/syntax"
	"slices"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Find and replace for Editor
// ---------------------------------------------------------------------------

// SearchOptions describes one find/replace request. Literal and regexp searches
// share one engine: a literal Query is escaped with regexp.QuoteMeta and compiled
// the same way, so match iteration, whole-word handling and case folding have one
// implementation.
type SearchOptions struct {
	Query     string
	Replace   string
	MatchCase bool
	WholeWord bool
	Regexp    bool

	// InSelection restricts ReplaceAll to the selection active when SetSearch ran.
	// No effect on FindNext, which searches the whole document: a find stopping at a
	// selection boundary would look like "no more matches".
	InSelection bool
}

// searchMatch is one match, as rune indices into a single logical line. The
// pattern is applied per line, so a match never spans lines, which keeps the
// per-line list usable directly by the drawing path.
type searchMatch struct {
	row      int
	startCol int
	endCol   int
}

// editorSearch is Editor's find/replace state: the compiled pattern, the match
// list derived from it, and the current match. matches is cached against the
// document version the scan ran on; Draw consults it every event, so rescanning
// per Draw would be an O(document) regexp sweep per keystroke.
type editorSearch struct {
	opts SearchOptions
	re   *regexp.Regexp
	// beginAnchored is set when the pattern holds `^` or `\A`, which a search over a
	// line's tail would wrongly satisfy at the tail's start (wholeWordLocs).
	beginAnchored bool

	matches    []searchMatch
	cur        int // index into matches, or -1 when nothing is current
	scanned    bool
	scanVer    uint64
	scanLen    int
	scanDocPtr *Document

	// selStart/selEnd bound an InSelection ReplaceAll, captured at SetSearch time
	// because replacing text moves the selection (from the second replacement on,
	// the first invalidated the range).
	selValid                 bool
	selStartRow, selStartCol int
	selEndRow, selEndCol     int
}

// SetSearch compiles opts into the active search and drops any previous match
// state. An invalid regexp is reported as an error and leaves no active search,
// so a half-typed pattern highlights nothing rather than the previous pattern's
// hits. An empty Query clears the search, like ClearSearch.
func (e *Editor) SetSearch(opts SearchOptions) error {
	if opts.Query == "" {
		e.ClearSearch()
		return nil
	}
	pat := opts.Query
	if !opts.Regexp {
		pat = regexp.QuoteMeta(pat)
	}
	// WholeWord is not a `\b` wrapper: RE2's \b is an ASCII word boundary, so
	// `\b@id\b` never matches "x = @id" and `\bcafé\b` never matches at all.
	// appendLineMatches filters on core.IsWordRune instead, the editor's own word
	// definition, so Find, Ctrl+F3 and word motion agree.
	if !opts.MatchCase {
		pat = `(?i)` + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		e.ClearSearch()
		return err
	}
	e.search = editorSearch{opts: opts, re: re, cur: -1, beginAnchored: hasBeginAnchor(pat)}
	if opts.InSelection && e.HasSelection() && !e.selBlock {
		sr, sc, er, ec := e.selectionBounds()
		e.search.selValid = true
		e.search.selStartRow, e.search.selStartCol = sr, sc
		e.search.selEndRow, e.search.selEndCol = er, ec
	}
	return nil
}

// ClearSearch drops the active search, its match list and its highlighting.
func (e *Editor) ClearSearch() { e.search = editorSearch{cur: -1} }

// HasSearch reports whether a search pattern is active.
func (e *Editor) HasSearch() bool { return e.search.re != nil }

// SearchOpts returns the options the active search was compiled from.
func (e *Editor) SearchOpts() SearchOptions { return e.search.opts }

// scanMatches brings the match list up to date with the document and returns it.
//
// Typing with a search active is the hot path: nothing clears the search when
// the Find dialog closes (F3 must keep working, SSMS parity), so every keystroke
// for the rest of the panel's life lands here through Draw. A full rescan is a
// regexp sweep over every line (123ms on a 20,000-line script), so a one-line
// edit resumes instead, as prefixStates.at does: the cache is exactly one version
// behind and the mutation touched exactly one line without changing the line
// count, so every other match keeps its row and only that line's run is
// re-scanned and spliced in. Anything else does the full scan.
func (e *Editor) scanMatches() []searchMatch {
	s := &e.search
	if s.re == nil {
		return nil
	}
	doc := e.doc
	switch {
	case !s.scanned || s.scanDocPtr != doc || s.scanLen != doc.Len():
		// A different document, or one that grew or shrank: matches are indexed by row,
		// so a changed line count invalidates every row below the edit.
		s.fullScan(doc)
	case s.scanVer == doc.Version():
		// Nothing has changed since the last scan.
	case s.scanVer+1 == doc.Version() && doc.dirtyTo == doc.dirtyFrom+1:
		// Exactly one mutation since, a single-line setLine (typing's path): only that
		// row's matches can have moved.
		s.rescanLine(doc, doc.dirtyFrom)
	default:
		s.fullScan(doc)
	}
	return s.matches
}

// fullScan rebuilds the whole match list from the document.
func (s *editorSearch) fullScan(doc *Document) {
	s.matches = s.matches[:0]
	for row, line := range doc.all() {
		s.matches = s.appendLineMatches(s.matches, row, string(line))
	}
	s.stamp(doc)
}

// rescanLine replaces row's run of matches in place, leaving every other row's
// entries and indices. The list stays sorted by row (matchSpansForLine's binary
// search and FindNext's ordering depend on it) because the replacement occupies
// exactly the old run's position.
func (s *editorSearch) rescanLine(doc *Document, row int) {
	start, end := rowRange(s.matches, row)
	fresh := s.appendLineMatches(nil, row, string(doc.Line(row)))
	s.matches = slices.Replace(s.matches, start, end, fresh...)
	s.stamp(doc)
}

// stamp records which document and version the match list now describes.
func (s *editorSearch) stamp(doc *Document) {
	s.scanned, s.scanVer, s.scanLen, s.scanDocPtr = true, doc.Version(), doc.Len(), doc
}

// appendLineMatches appends every match on one line's text to dst.
func (s *editorSearch) appendLineMatches(dst []searchMatch, row int, text string) []searchMatch {
	if text == "" {
		return dst
	}
	// Byte offsets from the regexp engine become rune indices once per line rather
	// than per match: every position Editor works in is a rune index, and a byte
	// offset reaching one lands mid-character on the first non-ASCII line.
	byteToRune := byteRuneIndex(text)
	for _, loc := range s.lineMatchLocs(text, false) {
		start, end := loc[0], loc[1]
		if byteToRune != nil {
			start, end = byteToRune[start], byteToRune[end]
		}
		if start == end {
			// A zero-width match (`^`, `\b`, `x*`) has nothing to select or replace, and Find
			// Next would stall on it forever.
			continue
		}
		dst = append(dst, searchMatch{row: row, startCol: start, endCol: end})
	}
	return dst
}

// lineMatchLocs returns the byte bounds of every match on one line's text, shaped
// like FindStringIndex's result, or FindStringSubmatchIndex's when submatches is
// set (Replace needs the groups; the per-Draw scan doesn't, and asking costs the
// regexp engine its fast path).
func (s *editorSearch) lineMatchLocs(text string, submatches bool) [][]int {
	if !s.opts.WholeWord || s.beginAnchored {
		var locs [][]int
		if submatches {
			locs = s.re.FindAllStringSubmatchIndex(text, -1)
		} else {
			locs = s.re.FindAllStringIndex(text, -1)
		}
		if s.opts.WholeWord {
			// Anchored: no tail search (see wholeWordLocs), so a rejected match can hide an
			// overlapping valid one; `^` binds a match to the line start, so it takes an
			// alternation like `^a|b` to show.
			locs = slices.DeleteFunc(locs, func(loc []int) bool {
				return !isWholeWord(text, loc[0], loc[1])
			})
		}
		return locs
	}
	return s.wholeWordLocs(text, submatches)
}

// wholeWordLocs finds the whole-word matches on text one at a time. A rejected
// candidate must not hide an overlapping valid one, so after a rejection the
// search resumes one rune past the candidate's start rather than at its end,
// which FindAll cannot do.
//
// Resuming searches text[pos:], where the regexp sees pos as the start of text.
// That changes only assertions evaluated at pos: `^`/`\A`, which beginAnchored
// keeps away from this path, and `\b`/`\B`. Those agree with the full line
// whenever the rune before pos is not a word rune; when it is, a match starting at
// pos fails isWholeWord's left check whatever the engine decided, and the search
// moves on.
func (s *editorSearch) wholeWordLocs(text string, submatches bool) [][]int {
	find := s.re.FindStringIndex
	if submatches {
		find = s.re.FindStringSubmatchIndex
	}
	var locs [][]int
	for pos := 0; pos <= len(text); {
		loc := find(text[pos:])
		if loc == nil {
			break
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += pos
			}
		}
		start, end := loc[0], loc[1]
		if start < end && isWholeWord(text, start, end) {
			locs = append(locs, loc)
			pos = end
			continue
		}
		if start == len(text) {
			break
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		pos = start + size
	}
	return locs
}

// isWholeWord reports whether text[start:end] has no word rune directly before or
// after it. Only outside neighbours count: `@id` in "x = @id" is a whole word
// though `@` is not a word rune, as `#tmp` and `@@ROWCOUNT` are.
func isWholeWord(text string, start, end int) bool {
	if r, _ := utf8.DecodeLastRuneInString(text[:start]); start > 0 && core.IsWordRune(r) {
		return false
	}
	if r, _ := utf8.DecodeRuneInString(text[end:]); end < len(text) && core.IsWordRune(r) {
		return false
	}
	return true
}

// hasBeginAnchor reports whether pat, a pattern regexp.Compile accepted, holds
// a start-of-text or start-of-line assertion anywhere.
func hasBeginAnchor(pat string) bool {
	re, err := syntax.Parse(pat, syntax.Perl)
	if err != nil {
		return true // the conservative answer: FindAll over the whole line
	}
	var walk func(*syntax.Regexp) bool
	walk = func(r *syntax.Regexp) bool {
		if r.Op == syntax.OpBeginText || r.Op == syntax.OpBeginLine {
			return true
		}
		return slices.ContainsFunc(r.Sub, walk)
	}
	return walk(re)
}

// rowRange returns the half-open range of row's matches in a row-sorted list,
// [start, start) when the row has none.
func rowRange(matches []searchMatch, row int) (start, end int) {
	start, _ = slices.BinarySearchFunc(matches, row, func(m searchMatch, r int) int {
		return m.row - r
	})
	end = start
	for end < len(matches) && matches[end].row == row {
		end++
	}
	return start, end
}

// byteRuneIndex maps every byte offset of s that starts a rune (plus len(s)) to
// that rune's index. Offsets inside a multi-byte rune stay zero; regexp match
// bounds always land on rune boundaries, so they are never read. Returns nil
// when s is pure ASCII (the identity, the common case in T-SQL), where the caller
// uses the byte offset directly; the map costs an allocation per line per rescan.
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

// hasMultiByte reports whether s holds any non-ASCII byte, i.e. whether byte
// offsets and rune indices can differ.
func hasMultiByte(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// MatchCount returns the number of matches in the document for the active search.
func (e *Editor) MatchCount() int { return len(e.scanMatches()) }

// MatchPosition returns the 1-based ordinal of the current match and the total
// count, for a "Match 2 of 7" readout. i is 0 when no match is current.
func (e *Editor) MatchPosition() (i, n int) {
	matches := e.scanMatches()
	if e.search.cur < 0 || e.search.cur >= len(matches) {
		return 0, len(matches)
	}
	return e.search.cur + 1, len(matches)
}

// WordAtCursor returns the identifier the caret sits in or next to, or "" on
// whitespace or punctuation, for Ctrl+F3, which needs the word without disturbing
// the selection.
func (e *Editor) WordAtCursor() string {
	line := e.doc.Line(e.cursorRow)
	if len(line) == 0 {
		return ""
	}
	col := core.Clamp(e.cursorCol, 0, len(line))
	// Prefer the word to the left when the caret sits just past its last rune, where
	// a double-click or word-jump leaves it.
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

// FindNext moves to the next match after the cursor (dir >= 0) or the last one
// before it (dir < 0), selects it, and scrolls it into view, wrapping around the
// document; it reports whether any match was found. The search starts from the
// cursor, not the previous match's index, so Find Next after clicking elsewhere
// continues from where the user is looking.
func (e *Editor) FindNext(dir int) bool {
	matches := e.scanMatches()
	if len(matches) == 0 {
		e.search.cur = -1
		return false
	}
	idx := -1
	if dir >= 0 {
		// From the cursor's column onward (from its end, with a match selected), so
		// repeated Find Next steps off the match just selected instead of re-selecting it.
		row, col := e.cursorRow, e.cursorCol
		if e.HasSelection() {
			// A selection's start is where the current match begins; search from its end so
			// the match under it is skipped.
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
			// Step back from the selection's start, so Find Previous on a selected match
			// moves off it.
			row, col, _, _ = e.selectionBounds()
		}
		for i, m := range slices.Backward(matches) {
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

// selectMatch makes m the editor's selection, cursor at its end, and scrolls it
// into view.
func (e *Editor) selectMatch(m searchMatch) {
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = m.row, m.startCol
	e.cursorRow, e.cursorCol = m.row, m.endCol
	e.clampCursor()
	e.desiredCol = e.cursorDisplayCol()
	// ensureCursorVisible scrolls sideways too, which a match far along a long line
	// needs: inside the viewport vertically, off it horizontally.
	e.ensureCursorVisible()
}

// ReplaceCurrent replaces the selected match with the active search's Replace
// text and advances to the next, reporting whether it replaced anything. It does
// nothing unless the selection is exactly a match (the SSMS/VS rule: on a fresh
// dialog Replace finds first and replaces on the second press). For a regexp
// search $1 group references are expanded (replacementFor); a literal replacement
// is inserted as-is.
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
	repl, ok := e.search.replacementFor(m, e.search.submatchesOf(e.doc.Line(m.row)))
	if !ok {
		return false
	}
	// replaceMatch rewrites one line and never changes the line count, so the step is
	// that one row: Replace/F3 down a large script is a held key.
	e.pushUndoSpan(m.row, m.row+1)
	e.replaceMatch(m, repl)
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

// lineSubmatches is one line's text with its regexp matches, submatches included,
// for expanding replacement templates.
type lineSubmatches struct {
	text       string
	locs       [][]int // FindStringSubmatchIndex-shaped, byte offsets into text
	byteToRune []int   // byteRuneIndex(text)
}

// submatchesOf scans line for replacementFor. Only a regexp search needs it; a
// literal one returns the zero value without scanning.
func (s *editorSearch) submatchesOf(line []rune) lineSubmatches {
	if !s.opts.Regexp {
		return lineSubmatches{}
	}
	text := string(line)
	return lineSubmatches{text: text, locs: s.lineMatchLocs(text, true), byteToRune: byteRuneIndex(text)}
}

// replacementFor returns the text that replaces m. A regexp template is expanded
// against the match in its line, never by re-running the pattern on the matched
// text cut out of it: there `\B`, `\b`, `^` and `$` see the cut's edges (`\Bing`
// fails on "ing" alone, so Replace counted a replacement and changed nothing), and
// an alternative matching again inside the cut was substituted twice. line must be
// the row's text before any replacement on it: Replace All rewrites a row right to
// left, and `$` or a trailing `\b` would see the already-replaced tail. ok is
// false when m is not a match of line, so nothing is replaced.
func (s *editorSearch) replacementFor(m searchMatch, line lineSubmatches) (repl string, ok bool) {
	if !s.opts.Regexp {
		return s.opts.Replace, true
	}
	for _, loc := range line.locs {
		start, end := loc[0], loc[1]
		if line.byteToRune != nil {
			start, end = line.byteToRune[start], line.byteToRune[end]
		}
		if start == m.startCol && end == m.endCol {
			return string(s.re.ExpandString(nil, s.opts.Replace, line.text, loc)), true
		}
	}
	return "", false
}

// replaceMatch substitutes repl for m's text in place. The caller owns the undo
// step: ReplaceAll pushes one for the whole run, so this must not push its own or
// Replace All would take one Ctrl+Z per occurrence.
func (e *Editor) replaceMatch(m searchMatch, repl string) {
	line := e.doc.Line(m.row)
	replRunes := []rune(e.expandTabs(repl))

	updated := make([]rune, 0, len(line)-(m.endCol-m.startCol)+len(replRunes))
	updated = append(updated, line[:m.startCol]...)
	updated = append(updated, replRunes...)
	updated = append(updated, line[m.endCol:]...)
	e.doc.setLine(m.row, updated)

	e.selecting = false
	e.cursorRow, e.cursorCol = m.row, m.startCol+len(replRunes)
	e.clampCursor()
}

// ReplaceAll replaces every match in the document (under InSelection with a
// selection active at SetSearch time, every match inside it) and returns how many
// it replaced. The whole run is one undo step, and each line is rewritten
// right-to-left so replacing one match doesn't shift later offsets on that line.
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
	// Every replacement is worked out before the first is made, so each expands
	// against its row as it was (replacementFor).
	repls := make([]string, 0, len(targets))
	lineRow, line := -1, lineSubmatches{}
	for _, m := range targets {
		if m.row != lineRow {
			lineRow, line = m.row, e.search.submatchesOf(e.doc.Line(m.row))
		}
		if repl, ok := e.search.replacementFor(m, line); ok {
			targets[len(repls)] = m
			repls = append(repls, repl)
		}
	}
	targets = targets[:len(repls)]
	if len(targets) == 0 {
		return 0
	}
	e.pushUndo()
	for i, target := range slices.Backward(targets) {
		e.replaceMatch(target, repls[i])
	}
	e.search.cur = -1
	e.selecting = false
	e.clampCursor()
	e.ensureCursorVisible()
	return len(targets)
}

// matchInScope reports whether m falls within an InSelection run's captured
// range; always true when the search isn't scoped to a selection.
func (e *Editor) matchInScope(m searchMatch) bool {
	s := &e.search
	if !s.opts.InSelection {
		return true
	}
	if !s.selValid {
		// InSelection was asked for with nothing selected: replacing the whole document
		// would be the opposite of what was asked.
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

// matchSpansForLine returns every match on row, once per drawn row. The list is
// sorted by row, so the row's slice is found by binary search rather than by
// walking a list that can hold a hit per line. The result aliases the cached list
// and must not be retained or modified. The current match is included like any
// other: it is also the selection, and the selection style wins in styleForRune.
func (e *Editor) matchSpansForLine(row int) []searchMatch {
	matches := e.scanMatches()
	if len(matches) == 0 {
		return nil
	}
	start, end := rowRange(matches, row)
	if start == end {
		return nil
	}
	return matches[start:end]
}

// ensureColumnVisible scrolls horizontally so the cursor's display column is
// inside the content area: the horizontal half of ensureCursorVisible (its only
// caller), separate only to keep the vertical arithmetic readable.
func (e *Editor) ensureColumnVisible() {
	if e.wrapMode {
		return
	}
	contentW := e.rect.W - e.gutterWidth()
	if contentW <= 0 {
		return
	}
	// In display columns: a line of wide characters scrolls twice as far per caret
	// step as an ASCII one, as the eye expects.
	col := e.cursorDisplayCol()
	if col < e.scrollCol {
		e.scrollCol = col
	} else if col >= e.scrollCol+contentW {
		e.scrollCol = col - contentW + 1
	}
	e.scrollCol = max(0, e.scrollCol)
}
