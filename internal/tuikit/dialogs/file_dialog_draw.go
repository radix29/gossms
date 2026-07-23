package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

func (d *FileDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	p := theme.Active()
	contentX := inner.X + 1
	contentW := inner.W - 2
	// -1: reserve the list's rightmost column for the scrollbar (see
	// listRect/DrawScrollbar below), so a full-width Modified timestamp
	// never gets its last digits overwritten by the scrollbar track/thumb.
	nameColW := nameColWidth(contentW - 1)

	d.pathField.SetBounds(contentX, inner.Y)
	d.pathField.Draw(s)

	headerY := inner.Y + 2
	headerStyle := theme.StyleGridHeader()
	core.FillRect(s, core.Rect{X: contentX, Y: headerY, W: contentW, H: 1}, ' ', headerStyle)
	core.DrawTextClipped(s, contentX, headerY, nameColW, headerStyle, "Name")
	core.DrawTextRight(s, contentX+nameColW+1, headerY, fileSizeColW, headerStyle, "Size")
	core.DrawTextRight(s, contentX+nameColW+1+fileSizeColW+1, headerY, fileModColW, headerStyle, "Modified")

	sepStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, contentX, inner.Y+3, contentW, sepStyle)

	lr := d.listRect()
	baseStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if d.listErr != "" {
		core.FillRect(s, lr, ' ', baseStyle)
		errStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawTextClipped(s, lr.X, lr.Y, lr.W, errStyle, d.listErr)
	} else {
		for row := 0; row < lr.H; row++ {
			idx := d.scroll + row
			y := lr.Y + row
			if idx >= len(d.entries) {
				core.FillRect(s, core.Rect{X: lr.X, Y: y, W: lr.W, H: 1}, ' ', baseStyle)
				continue
			}
			d.drawEntry(s, y, lr.X, nameColW, idx)
		}
		if len(d.entries) > lr.H {
			sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
			sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
			core.DrawScrollbar(s, lr.Right()-1, lr.Y, lr.H, len(d.entries), lr.H, d.scroll, sbStyle, sbThumb)
		}
	}

	d.nameField.SetBounds(contentX, inner.Y+fileListRows+5)
	d.nameField.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttonLabels(), d.btnFocus)
}

// drawEntry renders one list row at (x,y) — the Name column icon/marker,
// clipped to nameColW, plus the right-aligned Size/Modified columns.
func (d *FileDialog) drawEntry(s tcell.Screen, y, x, nameColW, idx int) {
	p := theme.Active()
	e := d.entries[idx]

	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	marker := "  "
	if idx == d.sel {
		marker = "▸ "
		if d.focus == ffList {
			st = theme.StyleSelected()
		} else {
			st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextHighlight)
		}
	}
	// +1: also paint the scrollbar gutter column so the row's selection
	// highlight reaches the dialog's right edge even when no scrollbar is
	// drawn over it this frame (see Draw's nameColW comment).
	core.FillRect(s, core.Rect{X: x, Y: y, W: nameColW + 1 + fileSizeColW + 1 + fileModColW + 1, H: 1}, ' ', st)

	icon, name := "📄", e.name
	if e.isDir {
		icon = "📁"
		name += "/"
	}
	core.DrawTextClipped(s, x, y, nameColW, st, marker+icon+" "+name)

	sizeText := formatFileSize(e.size)
	if e.isDir {
		sizeText = "DIR"
	}
	core.DrawTextRight(s, x+nameColW+1, y, fileSizeColW, st, sizeText)

	if e.name != ".." {
		modX := x + nameColW + 1 + fileSizeColW + 1
		core.DrawTextRight(s, modX, y, fileModColW, st, e.mod.Format("2006-01-02 15:04"))
	}
}
