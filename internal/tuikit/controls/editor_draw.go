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

	for row := 0; row < e.rect.H; row++ {
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

	if e.active {
		curX, curY := e.cursorScreenPos()
		if curY >= e.rect.Y && curY < e.rect.Y+e.rect.H &&
			curX >= contentX && curX < contentX+contentW {
			s.ShowCursor(curX, curY)
		}
	}
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
