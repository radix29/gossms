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
	bodyH := max(0, bodyBottom-bodyY)

	p.pageList.SetBounds(inner.X, bodyY, pageListWidth, bodyH)
	p.pageList.Draw(s)
	core.DrawVLine(s, inner.X+pageListWidth, bodyY, bodyH, sep)

	contentX := inner.X + pageListWidth + 2
	contentW := max(0, inner.Right()-contentX)
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
		core.DrawTextClipped(s, inner.X, msgY, inner.W, hintSt, p.hints)
	}

	p.DrawSeparator(s)
	activeIdx := -1
	if p.zone == zoneButtons {
		activeIdx = p.btnFocus
	}
	labels := p.buttonLabels()
	// Every button greys while applying except Cancel/Close, and that one only
	// while pressing it would stop the run (canCancelApply): activateButton
	// refuses the rest, and a button drawn live that does nothing on Enter
	// reads as a hang.
	var disabled []bool
	if p.applying {
		disabled = make([]bool, len(labels))
		for i, label := range labels {
			disabled[i] = !(p.canCancelApply() && (label == "Cancel" || label == "Close"))
		}
	}
	p.DrawButtonsGated(s, labels, activeIdx, disabled)
	// After the buttons, never before: on a clamped rect DrawButtonsGated
	// clears the whole button row, which would wipe the spinner.
	p.drawApplying(s, labels)

	if p.zone == zoneForm {
		if f := p.PageForm(p.current); f != nil {
			f.DrawOverlays(s)
		}
	}
}

// drawApplying paints the spinner and its label at the left end of the button
// row, opposite the buttons — where Connect puts its own — clipped short of
// the first button on a dialog clamped too narrow for both.
func (p *PropertySheet) drawApplying(s tcell.Screen, labels []string) {
	if !p.applying {
		return
	}
	x := p.InnerRect().X + 1
	y := p.ButtonRowY()
	avail := p.ButtonRowStartX(labels) - 1 - x
	sw := ApplyingSpinner.Width()
	if avail < sw {
		return
	}
	st := theme.StyleDialog()
	ApplyingSpinner.DrawSince(s, x, y, st, p.applyStarted)
	core.DrawTextClipped(s, x+sw+1, y, avail-sw-1, st, p.applyingLabel)
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

	contentY, contentH := y+2, max(0, h-2)
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
