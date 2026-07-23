package propsheet

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

func (p *PropertySheet) Draw(s tcell.Screen) {
	if !p.Visible() {
		return
	}
	p.recomputeSize()
	p.DrawBase(s)
	pal := theme.Active()
	inner := p.InnerRect()

	headerSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Text)
	core.DrawText(s, inner.X, inner.Y, headerSt, p.headerLeft)
	core.DrawTextRight(s, inner.X, inner.Y, inner.W, headerSt, p.headerRight)
	sep := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Border)
	core.DrawHLine(s, inner.X, inner.Y+1, inner.W, sep)

	bodyY := inner.Y + 2
	bodyBottom := p.ButtonRowY() - 2 // one row reserved for the hint/message line
	bodyH := core.Max(0, bodyBottom-bodyY)

	p.pageList.SetBounds(inner.X, bodyY, pageListWidth, bodyH)
	p.pageList.Draw(s)
	core.DrawVLine(s, inner.X+pageListWidth, bodyY, bodyH, sep)

	contentX := inner.X + pageListWidth + 2
	contentW := core.Max(0, inner.Right()-contentX)
	p.drawContent(s, contentX, bodyY, contentW, bodyH)

	msgY := bodyBottom
	if p.message != "" {
		st := headerSt
		if p.messageIsErr {
			st = tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Error)
		}
		core.DrawTextClipped(s, inner.X, msgY, inner.W, st, p.message)
	} else {
		hintSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.TextDim)
		hint := p.hints
		if p.applying {
			hint = "Applying…"
		}
		core.DrawTextClipped(s, inner.X, msgY, inner.W, hintSt, hint)
	}

	p.DrawSeparator(s)
	activeIdx := -1
	if p.zone == zoneButtons {
		activeIdx = p.btnFocus
	}
	p.DrawButtons(s, sheetButtonLabels, activeIdx)

	if p.zone == zoneForm {
		if f := p.PageForm(p.current); f != nil {
			f.DrawOverlays(s)
		}
	}
}

func (p *PropertySheet) drawContent(s tcell.Screen, x, y, w, h int) {
	if p.current < 0 || p.current >= len(p.pages) || h <= 0 {
		return
	}
	slot := &p.pages[p.current]
	pal := theme.Active()
	titleSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Text).Bold(true)
	core.DrawText(s, x, y, titleSt, slot.title)
	sep := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Border)
	core.DrawHLine(s, x, y+1, w, sep)

	contentY, contentH := y+2, core.Max(0, h-2)
	dimSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.TextDim)

	switch slot.state {
	case PageNotLoaded, PageLoading:
		core.DrawText(s, x, contentY, dimSt, "Loading…")
	case PageError:
		errSt := tcell.StyleDefault.Background(pal.DialogBg).Foreground(pal.Error)
		msg := "Error"
		if slot.err != nil {
			msg = "Error: " + slot.err.Error()
		}
		core.DrawTextClipped(s, x, contentY, w, errSt, msg)
		core.DrawText(s, x, contentY+2, dimSt, "Press F5 to retry.")
	case PageReady:
		if slot.form != nil {
			slot.form.SetBounds(x, contentY, w, contentH)
			slot.form.Draw(s)
		}
	}
}
