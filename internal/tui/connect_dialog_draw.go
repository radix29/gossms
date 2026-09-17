package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Drawing and layout
// ---------------------------------------------------------------------------

// layoutFields positions every control for the current pane mode. Both modes
// are computed here rather than in Draw, so the mouse code reads the same
// geometry that was painted.
func (d *ConnectDialog) layoutFields() {
	if want := d.Rect().W >= connectTwoPaneWidth; want != d.twoPane {
		d.twoPane = want
		d.rebuildFocusable()
	}
	inner := d.InnerRect()
	// The last row content may use: the separator sits one above the button
	// row, and nothing a dialog draws belongs at or below it.
	bottom := d.ButtonRowY() - 2

	rx := inner.X + 1
	d.paneRuleX = -1
	if d.twoPane {
		d.paneRuleX = inner.X + connectHistoryPane
		rx = d.paneRuleX + 2
		d.historyHeaderY = inner.Y
		listY := inner.Y + 2
		d.history.SetBounds(inner.X, listY, connectHistoryPane, max(0, bottom-listY+1))
	}
	d.rightX = rx
	d.tabRect = core.Rect{X: rx, Y: inner.Y, W: max(0, inner.Right()-rx), H: 1}

	row := inner.Y + 2
	d.fServer.SetBounds(rx, row)
	row++
	d.ddAuth.SetBounds(rx, row)
	row++
	d.fUser.SetBounds(rx, row)
	row++
	d.fPassword.SetBounds(rx, row)
	row++
	d.cbRemember.SetBounds(rx, row)
	row++
	d.fTenantID.SetBounds(rx, row)
	row++
	d.fClientID.SetBounds(rx, row)
	row++
	d.fDatabase.SetBounds(rx, row)
	row++
	d.ddEncrypt.SetBounds(rx, row)
	row++
	d.cbTrust.SetBounds(rx, row)
	row++
	d.fHostCert.SetBounds(rx, row)

	// Same on-screen width as the Password field's whole visible box (label +
	// brackets + content), from real widget geometry.
	w := d.rightWidth()

	// The Custom Properties section is anchored to the bottom of the pane, so
	// it stays put as the tab above it changes height.
	d.extraPropsLabelY = bottom - 3
	d.fExtraProps.SetBounds(rx, d.extraPropsLabelY+1, w, 3)

	// The Connection String tab's own editor fills everything the shared
	// section leaves, under its label.
	d.connStrLabelY = inner.Y + 2
	d.fConnStrPreview.SetBounds(rx, d.connStrLabelY+1, w, max(1, d.extraPropsLabelY-1-(d.connStrLabelY+1)))
}

// rightWidth is the tabbed pane's content width, taken from real widget
// geometry rather than recomputed from the column constants.
func (d *ConnectDialog) rightWidth() int {
	return d.fPassword.InputX() + d.fPassword.Width() + 2 - d.rightX
}

// drawButtonRow paints both button groups. The right-aligned one goes first:
// it is the call that clears the row on a clamped rect.
func (d *ConnectDialog) drawButtonRow(s tcell.Screen) {
	gated := d.buttonsDisabled()
	n := len(connectLeftButtons)
	d.DrawButtonsGated(s, connectRightButtons, d.btnFocus-n, gated[n:])
	d.DrawButtonsAtGated(s, d.buttonRowLeftX(), connectLeftButtons, d.btnFocus, gated[:n])
}

// tabSegments computes each tab's extent; drawing and hit-testing share it so
// clicks land where they look.
func (d *ConnectDialog) tabSegments() [][]controls.TabSegment {
	widths := make([][]int, len(connectTabLabels))
	for i, label := range connectTabLabels {
		widths[i] = []int{controls.TabLabelWidth(label)}
	}
	return controls.TabStripSegments(d.tabRect.X, widths, d.tabRect.Right()+1)
}

// Draw renders the dialog.
func (d *ConnectDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layoutFields()

	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)

	if d.twoPane {
		d.drawHistoryPane(s, labelStyle)
	}
	d.drawTabBar(s)

	switch d.tab {
	case connectTabString:
		core.DrawText(s, d.rightX, d.connStrLabelY, labelStyle, "Connection String:")
		d.fConnStrPreview.Draw(s)
	default:
		d.fServer.Draw(s)
		d.ddAuth.Draw(s)
		d.fUser.Draw(s)
		d.fPassword.Draw(s)
		d.cbRemember.Draw(s)
		d.fTenantID.Draw(s)
		d.fClientID.Draw(s)
		d.fDatabase.Draw(s)
		d.ddEncrypt.Draw(s)
		d.cbTrust.Draw(s)
		d.fHostCert.Draw(s)
	}

	d.drawSection(s, "Custom Properties", d.extraPropsLabelY)
	d.fExtraProps.Draw(s)

	d.DrawSeparator(s)
	d.drawButtonRow(s)
	// After the buttons, never before: on a clamped rect DrawButtonsGated
	// clears the whole button row, which would wipe the spinner.
	d.drawConnecting(s, labelStyle)

	// Drawn last, so neither dropdown's open list is painted over by the
	// fields and buttons below it.
	if d.tab == connectTabProperties {
		d.ddAuth.DrawOverlay(s)
		d.ddEncrypt.DrawOverlay(s)
	}
}

// drawHistoryPane renders the saved-connection list, its count header and the
// vertical rule dividing it from the tabbed pane.
func (d *ConnectDialog) drawHistoryPane(s tcell.Screen, labelStyle tcell.Style) {
	p := theme.Active()
	core.DrawTextClipped(s, d.InnerRect().X, d.historyHeaderY, connectHistoryPane,
		labelStyle.Bold(true), fmt.Sprintf("Recent Connections (%d)", len(d.historyConns)))
	ruleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	for y := d.InnerRect().Y; y <= d.ButtonRowY()-2; y++ {
		core.DrawText(s, d.paneRuleX, y, ruleStyle, "│")
	}
	d.history.Draw(s)
}

// drawTabBar renders the right pane's two tabs and the rule under them, styled
// like the query panel's result tabs.
func (d *ConnectDialog) drawTabBar(s tcell.Screen) {
	p := theme.Active()
	barStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	core.FillRect(s, d.tabRect, ' ', barStyle)
	for i, seg := range d.tabSegments() {
		style := barStyle
		if connectTab(i) == d.tab {
			style = tcell.StyleDefault.Background(p.BorderActive).Foreground(color.White).Bold(true)
		}
		core.DrawText(s, seg[0].X, d.tabRect.Y, style, " "+connectTabLabels[i]+" ")
	}
	ruleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, d.rightX, d.tabRect.Y+1, d.rightWidth(), ruleStyle)
}

// drawSection draws a titled horizontal rule across the tabbed pane.
func (d *ConnectDialog) drawSection(s tcell.Screen, title string, y int) {
	p := theme.Active()
	ruleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	titleStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextHighlight)
	core.DrawHLine(s, d.rightX, y, d.rightWidth(), ruleStyle)
	core.DrawText(s, d.rightX+1, y, titleStyle, " "+title+" ")
}

// drawConnecting paints the spinner and its label in the gap between Delete
// and the right-hand button group.
//
// The label is clipped to what is left before that group. A one-pane dialog is
// only just wide enough for the four buttons, and drawn last — after
// DrawButtonsGated, so the spinner survives a clamped rect's row clear — an
// unclipped "Connecting..." painted over the leading '[' of Reset.
func (d *ConnectDialog) drawConnecting(s tcell.Screen, style tcell.Style) {
	if !d.connecting {
		return
	}
	// Between the two groups: Delete holds the left end of the row.
	x := d.buttonRowLeftX() + d.ButtonRowWidth(connectLeftButtons) + 2
	y := d.ButtonRowY()
	connectSpinner.DrawSince(s, x, y, style, d.connectStarted)
	lx := x + connectSpinner.Width() + 1
	core.DrawText(s, lx, y, style,
		core.Truncate(d.connectLabel, max(0, d.ButtonRowStartX(connectRightButtons)-2-lx)))
}
