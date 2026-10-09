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
	// -1: reserve the list's rightmost column for the scrollbar (see listRect), so a
	// full-width Modified timestamp isn't overwritten by the track/thumb.
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
	// busy wins over listErr: it is set only during a FileSystem call, and what it
	// replaces (the previous listing or error) is about to be superseded.
	note := d.busy
	if note == "" {
		note = d.listErr
	}
	if note != "" {
		core.FillRect(s, lr, ' ', baseStyle)
		noteStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		// Wrapped across the list area, not clipped to one line: a listing error is a
		// whole sentence in a ~70-column box, and a one-line clip cut the reason off
		// mid-word ("before SQL Server 2017 the server lists", the pre-2017 xp_dirtree
		// refusal).
		for i, line := range core.WrapTextLimit(note, lr.W, lr.H) {
			core.DrawTextClipped(s, lr.X, lr.Y+i, lr.W, noteStyle, line)
		}
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
	// +1: also paint the scrollbar gutter so the selection highlight reaches the
	// dialog's right edge when no scrollbar is drawn (see Draw's nameColW comment).
	core.FillRect(s, core.Rect{X: x, Y: y, W: nameColW + 1 + fileSizeColW + 1 + fileModColW + 1, H: 1}, ' ', st)

	icon, name := "📄", e.Name
	if e.IsDir {
		icon = "📁"
		name += d.FileSystem().Separator()
	}
	core.DrawTextClipped(s, x, y, nameColW, st, marker+icon+" "+name)

	// An unreported size or timestamp is left blank: a FileSystem that can't supply
	// them (a pre-2017 SQL Server via xp_dirtree) would otherwise date every entry
	// 0001-01-01 and call every file empty, which reads as fact, not absence.
	sizeText := ""
	switch {
	case e.IsDir:
		sizeText = "DIR"
	case !e.SizeUnknown:
		sizeText = formatFileSize(e.Size)
	}
	core.DrawTextRight(s, x+nameColW+1, y, fileSizeColW, st, sizeText)

	if e.Name != ".." && !e.ModTime.IsZero() {
		modX := x + nameColW + 1 + fileSizeColW + 1
		core.DrawTextRight(s, modX, y, fileModColW, st, e.ModTime.Format("2006-01-02 15:04"))
	}
}
