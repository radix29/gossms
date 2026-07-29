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

	for row := 0; row < e.contentH(); row++ {
		lineIdx := e.scrollRow + row
		y := e.rect.Y + row

		// Gutter
		if gw > 0 {
			core.FillRect(s, core.Rect{X: e.rect.X, Y: y, W: gw, H: 1}, ' ', gutterStyle)
			if lineIdx < len(e.lines) {
				num := core.Itoa(lineIdx + 1)
				gx := e.rect.X + gw - 1 - len(num)
				core.DrawText(s, gx, y, gutterStyle, num)
			}
		}

		if lineIdx >= len(e.lines) {
			continue
		}
		line := e.lines[lineIdx]
		selStart, selEnd, hasSel := e.selectionRangeForLine(lineIdx)

		if e.highlight != nil {
			runs := e.highlight(e.lines, lineIdx)
			e.drawHighlighted(s, contentX, y, contentW, line, runs, selStart, selEnd, hasSel)
		} else {
			// Plain
			selStyle := theme.StyleSelected()
			for col := 0; col < contentW; col++ {
				ch := ' '
				ci := e.scrollCol + col
				if ci < len(line) {
					ch = line[ci]
				}
				st := bgStyle
				if hasSel && ci >= selStart && ci < selEnd {
					st = selStyle
				}
				s.SetContent(contentX+col, y, ch, nil, st)
			}
		}
	}

	e.drawScrollbar(s, p, len(e.lines))
	e.drawScrollbarH(s, p)

	if e.active {
		curX, curY := e.cursorScreenPos()
		if curY >= e.rect.Y && curY < e.rect.Y+e.contentH() &&
			curX >= contentX && curX < contentX+contentW {
			s.ShowCursor(curX, curY)
		}
	}
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
// total/visible/offset describing it, or ok false when the longest line
// already fits (or there's no room for a bar). The unit throughout is rune
// columns, matching the editor's own horizontal model — scrollCol, cursorCol
// and the draw loop all index lines by rune, one screen cell each — rather
// than display width. That makes the track width and the visible count the
// same number, which is why core.HandleScrollbarDragH can drive it directly.
func (e *Editor) hScrollbar() (x, y, w, total, offset int, ok bool) {
	if e.wrapMode || e.rect.H < 2 {
		// Word wrap never scrolls sideways — buildVisualLines guarantees no
		// segment is wider than the content area.
		return 0, 0, 0, 0, 0, false
	}
	gw := e.gutterWidth()
	w = e.rect.W - gw
	total = e.longestLineLen()
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

// longestLineLen is the rune length of the longest line, measured over the
// whole buffer rather than just the visible window: a bar sized off only
// what's on screen would resize itself, and appear and vanish, as the
// editor scrolled vertically. Each line's length is O(1), so this is a
// cheap scan even for a large Messages buffer.
func (e *Editor) longestLineLen() int {
	longest := 0
	for _, line := range e.lines {
		if len(line) > longest {
			longest = len(line)
		}
	}
	return longest
}

// drawScrollbarH renders the horizontal scrollbar along the editor's bottom
// row when the longest line doesn't fit. Unlike the vertical bar — which
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
	x = e.rect.X + e.gutterWidth() + (e.cursorCol - e.scrollCol)
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
// continuing across a real line break.
func (e *Editor) drawWrapped(s tcell.Screen, contentX, contentW, gw int, gutterStyle tcell.Style) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)
	selStyle := theme.StyleSelected()

	vls := e.buildVisualLines(contentW)

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

		line := e.lines[vl.row]
		selStart, selEnd, hasSel := e.selectionRangeForLine(vl.row)

		for col := 0; col < contentW; col++ {
			ci := vl.start + col
			ch := rune(' ')
			st := bgStyle
			if ci < vl.end {
				ch = line[ci]
				if hasSel && ci >= selStart && ci < selEnd {
					st = selStyle
				}
			}
			s.SetContent(contentX+col, y, ch, nil, st)
		}
	}

	e.drawScrollbar(s, p, len(vls))

	if e.active {
		vi := visualIndexForCursor(vls, e.cursorRow, e.cursorCol)
		screenRow := vi - e.scrollRow
		if vi < len(vls) && screenRow >= 0 && screenRow < e.rect.H {
			curX := contentX + (e.cursorCol - vls[vi].start)
			if curX >= contentX && curX < contentX+contentW {
				s.ShowCursor(curX, e.rect.Y+screenRow)
			}
		}
	}
}

func (e *Editor) drawHighlighted(s tcell.Screen, x, y, w int, line []rune, runs []ColorRun, selStart, selEnd int, hasSel bool) {
	p := theme.Active()
	bgStyle := tcell.StyleDefault.Background(p.EditorBg).Foreground(p.Text)
	selStyle := theme.StyleSelected()

	// Build a per-column style map
	styles := make([]tcell.Style, len(line))
	for i := range styles {
		styles[i] = bgStyle
	}
	for _, run := range runs {
		for j := run.Start; j < run.Start+run.Len && j < len(styles); j++ {
			styles[j] = run.Style
		}
	}
	if hasSel {
		for j := core.Max(0, selStart); j < selEnd && j < len(styles); j++ {
			styles[j] = selStyle
		}
	}

	for col := 0; col < w; col++ {
		ci := e.scrollCol + col
		ch := ' '
		st := bgStyle
		if hasSel && ci >= selStart && ci < selEnd {
			st = selStyle
		}
		if ci < len(line) {
			ch = line[ci]
			st = styles[ci]
		}
		s.SetContent(x+col, y, ch, nil, st)
	}
}
