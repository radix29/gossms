package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Rendering for Editor: plain, wrapped, and syntax-highlighted line drawing
// ---------------------------------------------------------------------------

const gutterW = 5 // " NNN "

// Draw renders the editor.
func (e *Editor) Draw(s tcell.Screen) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)
	gutterStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.EditorLineNum)
	gw := e.gutterWidth()
	contentX := e.rect.X + gw
	contentW := e.rect.W - gw

	core.FillRect(s, e.rect, ' ', bgStyle)

	if e.wrapMode {
		e.drawWrapped(s, contentX, contentW, gw, gutterStyle)
		return
	}

	selStyle := theme.StyleSelected()
	// Hoisted: contentH consults the horizontal scrollbar, which measures the
	// widest line in the buffer, so leaving it in the loop condition costs
	// that lookup once per drawn row.
	contentH := e.contentH()
	for row := 0; row < contentH; row++ {
		lineIdx := e.scrollRow + row
		y := e.rect.Y + row

		// Gutter
		if gw > 0 {
			core.FillRect(s, core.Rect{X: e.rect.X, Y: y, W: gw, H: 1}, ' ', gutterStyle)
			if lineIdx < e.doc.Len() {
				num := core.Itoa(lineIdx + 1)
				gx := e.rect.X + gw - 1 - len(num)
				core.DrawText(s, gx, y, gutterStyle, num)
			}
		}

		if lineIdx >= e.doc.Len() {
			continue
		}
		line := e.doc.Line(lineIdx)
		selStart, selEnd, hasSel := e.selectionRangeForLine(lineIdx)

		spec := lineRow{
			x: contentX, y: y, w: contentW, fromCol: e.scrollCol,
			line: line, endRune: len(line),
			def: bgStyle, sel: selStyle,
			selStart: selStart, selEnd: selEnd, hasSel: hasSel,
		}
		if e.highlight != nil {
			spec.styles = e.runStyles(e.highlight(e.doc, lineIdx), len(line), bgStyle)
		}
		drawLineRow(s, spec)
	}

	e.drawScrollbar(s, p, e.doc.Len())
	e.drawScrollbarH(s, p)

	if e.active {
		curX, curY := e.cursorScreenPos()
		if curY >= e.rect.Y && curY < e.rect.Y+contentH &&
			curX >= contentX && curX < contentX+contentW {
			s.ShowCursor(curX, curY)
		}
	}
}

// lineRow is one call's worth of arguments to drawLineRow, grouped rather
// than passed positionally because there are eleven of them and several are
// ints that would otherwise be trivial to transpose. Passed by value, so
// grouping them costs no allocation.
type lineRow struct {
	x, y, w int
	// fromCol is the terminal column of the line that lands at x — the
	// horizontal scroll offset in non-wrap mode, the wrap segment's starting
	// column in wrap mode.
	fromCol int

	line []rune
	// endRune bounds the drawn text: runes at or past it are treated as
	// past-end-of-line, which is how a wrap segment stops at its own end
	// instead of bleeding into the next one.
	endRune int

	// styles, when non-nil, gives a per-rune style indexed like line.
	// Otherwise runs is scanned per column — see runStyles and styleAt for
	// which call site wants which.
	styles []tcell.Style
	runs   []ColorRun

	def, sel         tcell.Style
	selStart, selEnd int
	hasSel           bool
}

// styleForRune resolves the style of the rune at index i: an active
// selection wins over the highlighter, which wins over the default.
func (r *lineRow) styleForRune(i int) tcell.Style {
	if r.hasSel && i >= r.selStart && i < r.selEnd {
		return r.sel
	}
	if r.styles != nil {
		if i < len(r.styles) {
			return r.styles[i]
		}
		return r.def
	}
	if r.runs != nil {
		return styleAt(r.runs, i, r.def)
	}
	return r.def
}

// drawLineRow renders the [fromCol, fromCol+w) terminal-column window of one
// logical line.
//
// This is the whole of Editor's rune-index-to-column mapping on the drawing
// side. It walks the line accumulating core.RuneWidth rather than assuming a
// column per rune, which is what a CJK or emoji character needs: as one
// glyph over two cells it shifts every rune after it one column right, and
// counting runes instead put the rest of the line — and the caret — one
// column left of where it rendered.
//
// Columns past endRune count one virtual rune each, so a linear selection
// running on to the next line still paints the single extra cell that shows
// the line break itself as selected.
//
// A wide rune clipped by either edge of the window is drawn as blanks rather
// than as half a glyph. tcell owns both cells of a double-width character —
// it writes the second one itself as a continuation of the first — so
// emitting half of one leaves the terminal drawing a full-width glyph over a
// neighbouring cell.
func drawLineRow(s tcell.Screen, r lineRow) {
	if r.w <= 0 {
		return
	}
	n := core.Min(r.endRune, len(r.line))

	// Skip whatever lies entirely left of the window.
	i, col := 0, 0
	for i < n {
		rw := core.RuneWidth(r.line[i])
		if col+rw > r.fromCol {
			break
		}
		col += rw
		i++
	}

	sx := 0
	// A wide rune straddling the left edge shows only its right-hand cell,
	// which is not a glyph — blank it.
	if i < n && col < r.fromCol {
		st := r.styleForRune(i)
		for c := r.fromCol; c < col+core.RuneWidth(r.line[i]) && sx < r.w; c++ {
			s.SetContent(r.x+sx, r.y, ' ', nil, st)
			sx++
		}
		i++
	}

	for sx < r.w {
		st := r.styleForRune(i)
		if i >= n {
			// Past the end of the drawn range: one virtual column per cell,
			// so index and column stay in step for the selection test above.
			s.SetContent(r.x+sx, r.y, ' ', nil, st)
			sx++
			i++
			continue
		}
		ch := r.line[i]
		rw := core.RuneWidth(ch)
		if rw == 0 {
			// A combining mark occupies the cell its base rune already
			// claimed; drawing it on its own would consume a column that
			// isn't there and shift the rest of the line.
			i++
			continue
		}
		if sx+rw > r.w {
			// Clipped by the right edge — blanks, never half a glyph.
			for ; sx < r.w; sx++ {
				s.SetContent(r.x+sx, r.y, ' ', nil, st)
			}
			break
		}
		s.SetContent(r.x+sx, r.y, ch, nil, st)
		sx += rw
		i++
	}
}

// runStyles expands a line's ColorRuns into a per-rune style map, reusing
// the scratch buffer rather than allocating one per line per Draw — Draw
// runs on every event the app processes, so a fresh slice per row was one
// allocation per row per keystroke. Later runs win, matching styleAt.
//
// Valid only until the next call; nothing may retain the result.
func (e *Editor) runStyles(runs []ColorRun, n int, def tcell.Style) []tcell.Style {
	if cap(e.styleScratch) < n {
		e.styleScratch = make([]tcell.Style, n)
	}
	styles := e.styleScratch[:n]
	for i := range styles {
		styles[i] = def
	}
	for _, run := range runs {
		for j := core.Max(0, run.Start); j < run.Start+run.Len && j < n; j++ {
			styles[j] = run.Style
		}
	}
	return styles
}

// drawScrollbar renders a DataGrid-style vertical scrollbar over the
// editor's rightmost screen column when the content (total lines in plain
// mode, visual rows in wrap mode — see drawWrapped) doesn't fit in the
// visible height. There's no reserved border column here (same as
// DataGrid), so this overdraws whatever was in that column; only called
// when there's actually something to scroll, matching
// DataGrid/TreeView/ListBox's own call-site guard.
func (e *Editor) drawScrollbar(s tcell.Screen, p *theme.Palette, total int) {
	h := e.contentH()
	if total <= h || h <= 0 {
		return
	}
	sbStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Border)
	sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
	core.DrawScrollbar(s, e.rect.Right()-1, e.rect.Y, h, total, h, e.scrollRow, sbStyle, sbThumb)
}

// hScrollbar returns the horizontal scrollbar's screen span and the
// total/visible/offset describing it, or ok false when the widest line
// already fits (or there's no room for a bar).
//
// The unit throughout is terminal columns — matching scrollCol, the caret's
// x, and drawLineRow's window, all of which are columns rather than rune
// counts. That keeps the track width and the visible count the same number,
// which is why core.HandleScrollbarDragH can drive it directly.
func (e *Editor) hScrollbar() (x, y, w, total, offset int, ok bool) {
	if e.wrapMode || e.rect.H < 2 {
		// Word wrap never scrolls sideways — buildVisualLines guarantees no
		// segment is wider than the content area.
		return 0, 0, 0, 0, 0, false
	}
	gw := e.gutterWidth()
	w = e.rect.W - gw
	total = e.doc.maxDisplayWidth()
	if w <= 0 || total <= w {
		return 0, 0, 0, 0, 0, false
	}
	return e.rect.X + gw, e.rect.Y + e.rect.H - 1, w, total, e.scrollCol, true
}

// hScrollbarVisible reports whether the bottom row is currently a scrollbar
// rather than a line of text — see contentH.
func (e *Editor) hScrollbarVisible() bool {
	_, _, _, _, _, ok := e.hScrollbar()
	return ok
}

// drawScrollbarH renders the horizontal scrollbar along the editor's bottom
// row when the widest line doesn't fit. Unlike the vertical bar — which
// overdraws the rightmost content column — this one gets a row of its own
// (see contentH): a whole line of hidden text is too much to give up, and
// the bar is what tells the user there's more text off to the right at all.
// The gutter keeps its own column range; the track starts where the text
// does, so the thumb's position matches the text it describes.
func (e *Editor) drawScrollbarH(s tcell.Screen, p *theme.Palette) {
	x, y, w, total, offset, ok := e.hScrollbar()
	if !ok {
		return
	}
	sbStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Border)
	sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
	core.DrawScrollbarH(s, x, y, w, total, w, offset, sbStyle, sbThumb)
}

// cursorScreenPos returns the screen coordinates of the text cursor (valid
// only when it's actually within the visible rect — callers that draw it,
// like Draw above, still bounds-check). Also used to position the
// Cut/Copy/Paste context menu when it's opened via Ctrl+Space instead of a
// right-click (see HandleKey).
func (e *Editor) cursorScreenPos() (x, y int) {
	x = e.rect.X + e.gutterWidth() + (e.cursorDisplayCol() - e.scrollCol)
	y = e.rect.Y + (e.cursorRow - e.scrollRow)
	return x, y
}

// drawWrapped renders the editor in word-wrap mode: each screen row shows
// one soft-wrapped segment of a logical line, e.scrollRow counts visual
// rows (not logical lines), and there is no horizontal scrolling — every
// segment is drawn starting at column 0 of the content area, since
// wrapSegments guarantees no segment is wider than contentW.
//
// Selection highlighting only covers actual characters, never the blank
// padding after a short segment — unlike non-wrap mode, which highlights
// one extra cell past a selected line's end to show the selection
// continuing across a real line break. That's what clamping selEnd to the
// segment's end below achieves.
func (e *Editor) drawWrapped(s tcell.Screen, contentX, contentW, gw int, gutterStyle tcell.Style) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)
	selStyle := theme.StyleSelected()

	vls := e.buildVisualLines(contentW)

	// Highlighter runs are per logical line, so they're fetched when vl.row
	// changes rather than per visual row.
	runRow, runs := -1, []ColorRun(nil)

	for screenRow := 0; screenRow < e.rect.H; screenRow++ {
		vi := e.scrollRow + screenRow
		y := e.rect.Y + screenRow

		if gw > 0 {
			core.FillRect(s, core.Rect{X: e.rect.X, Y: y, W: gw, H: 1}, ' ', gutterStyle)
		}

		if vi >= len(vls) {
			continue
		}
		vl := vls[vi]

		// Only the first visual row of each logical line gets a line
		// number in the gutter; continuation rows leave it blank.
		if gw > 0 && (vi == 0 || vls[vi-1].row != vl.row) {
			num := core.Itoa(vl.row + 1)
			gx := e.rect.X + gw - 1 - len(num)
			core.DrawText(s, gx, y, gutterStyle, num)
		}

		line := e.doc.Line(vl.row)
		selStart, selEnd, hasSel := e.selectionRangeForLine(vl.row)
		if hasSel {
			selEnd = core.Min(selEnd, vl.end)
		}

		if e.highlight != nil && vl.row != runRow {
			runRow, runs = vl.row, e.highlight(e.doc, vl.row)
		}

		spec := lineRow{
			x: contentX, y: y, w: contentW,
			fromCol: core.ColumnOfRune(line, vl.start),
			line:    line, endRune: vl.end,
			def: bgStyle, sel: selStyle,
			selStart: selStart, selEnd: selEnd, hasSel: hasSel,
		}
		if e.highlight != nil {
			// Runs are scanned per column rather than expanded into a
			// per-rune map: wrap mode's one call site that shows query data
			// is DataGrid's cell viewer, where a single logical line can be
			// a whole varchar(max) document and only ~15 rows of it are on
			// screen. Materialising a style for every rune of that line
			// would be work proportional to the cell, not the viewport.
			spec.runs = runs
		}
		drawLineRow(s, spec)
	}

	e.drawScrollbar(s, p, len(vls))

	if e.active {
		vi := visualIndexForCursor(vls, e.cursorRow, e.cursorCol)
		screenRow := vi - e.scrollRow
		if vi < len(vls) && screenRow >= 0 && screenRow < e.rect.H {
			line := e.doc.Line(vls[vi].row)
			curX := contentX + core.RunesWidth(line[vls[vi].start:core.Min(e.cursorCol, len(line))])
			if curX >= contentX && curX < contentX+contentW {
				s.ShowCursor(curX, e.rect.Y+screenRow)
			}
		}
	}
}

// styleAt returns the style a highlighter assigned to the rune at index i,
// or def where no run covers it. Later runs win, matching runStyles' map,
// which overwrites as it walks runs in order.
func styleAt(runs []ColorRun, i int, def tcell.Style) tcell.Style {
	st := def
	for _, run := range runs {
		if i >= run.Start && i < run.Start+run.Len {
			st = run.Style
		}
	}
	return st
}
