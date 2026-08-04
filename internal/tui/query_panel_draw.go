package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Draw renders the title bar, editor, splitter, results tab bar, and grid.
func (p *QueryPanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if p.editorHasFocus() {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
	}
	core.FillRect(s, core.Rect{X: p.rect.X, Y: p.rect.Y, W: p.rect.W, H: 1}, ' ', titleStyle)
	core.DrawTextClipped(s, p.rect.X+1, p.rect.Y, p.rect.W-2, titleStyle, p.connInfoText())

	p.editor.Draw(s)
	p.splitter.SetActive(p.resultsHasFocus())
	p.splitter.Draw(s)
	p.drawTabBar(s)
	p.updateResultsStatus()
	switch {
	case p.onMessagesTab():
		p.messages.Draw(s)
		p.drawResultsStatus(s)
	case p.planTabActive():
		p.planView.Draw(s)
		p.drawResultsStatus(s)
		p.planView.DrawOverlay(s)
	case p.textTabActive():
		p.resultsText.Draw(s)
		p.drawResultsStatus(s)
	default:
		p.results.Draw(s)
		p.results.DrawOverlay(s)
	}
	// Drawn last, after the results grid's own overlay — see the "overlays
	// drawn last" rule in tuikit/README.md.
	p.editor.DrawOverlay(s)
}

// connInfoText builds the bar above the editor: "server | user | db",
// matching SSMS's connection status bar — the one place in the panel that
// says what this query is actually running against, distinct from the
// PanelManager tab bar's title just above it.
func (p *QueryPanel) connInfoText() string {
	if p.conn == nil {
		return "(not connected)"
	}
	user := p.conn.Opts.User
	if user == "" {
		user = config.AuthMethodName(p.conn.Opts.AuthMethod)
	}
	text := fmt.Sprintf("%s | %s | %s", p.conn.Opts.Server, user, p.database)
	if !p.app.isConnected(p.conn) {
		text += " (disconnected)"
	}
	return text
}

// notConnectedMessage is runQuery/runEstimatedPlan's resultsNotice when
// isConnected(p.conn) is false — distinguishing a panel that was connected
// and then had its connection silently dropped (p.conn is still this
// panel's own *db.ServerConn, just no longer open — see Reconnect) from one
// that was never connected in the first place, since Query > Reconnect only
// has anything to redial in the former case.
func (p *QueryPanel) notConnectedMessage() string {
	if p.conn != nil {
		return "Not connected — use Query > Reconnect"
	}
	return notConnectedMessage
}

// updateResultsStatus pushes the current status line into the results grid,
// recomputed on every Draw so it tracks row/column navigation and, while a
// query is executing, ticks live off execStart (see tickExecuting). The
// other three tabs get the same line from drawResultsStatus.
func (p *QueryPanel) updateResultsStatus() {
	p.results.SetStatus(p.resultsStatusText())
}

// drawResultsStatus paints the status line for a tab the results grid isn't
// drawing — Messages, Results To Text, and Execution Plan. Without it those
// tabs showed nothing at all: the line lives on the DataGrid, which isn't on
// screen for any of them, so elapsed time, row counts and the live
// "Executing..." counter all disappeared the moment the tab changed.
func (p *QueryPanel) drawResultsStatus(s tcell.Screen) {
	if p.statusRect.H != 1 {
		return
	}
	core.FillRect(s, p.statusRect, ' ', resultsStatusStyle)
	core.DrawTextRight(s, p.statusRect.X+1, p.statusRect.Y, p.statusRect.W-2,
		resultsStatusStyle, p.resultsStatusText())
}

// resultsStatusText builds the results area's status line for whichever tab
// is active. resultsNotice, when set, takes priority — see its field doc.
func (p *QueryPanel) resultsStatusText() string {
	if p.resultsNotice != "" {
		return p.resultsNotice
	}
	if p.executing {
		return formatElapsedHMS(time.Since(p.execStart)) + " | Executing..."
	}
	if p.result == nil {
		// Estimated plan: nothing ran, so there's no elapsed time or row
		// count to report — and the previous run's, left over from before
		// setEstimatedPlan cleared p.result, would be a lie.
		if p.planView != nil {
			return "Estimated execution plan"
		}
		return ""
	}
	elapsed := formatElapsedHMS(p.result.Elapsed)
	switch {
	case p.onMessagesTab():
		return fmt.Sprintf("%s | %d message(s)", elapsed, len(p.result.Messages))
	case p.planTabActive():
		return elapsed + " | Actual execution plan"
	}
	set, ok := p.activeResultSet()
	if !ok {
		return elapsed
	}
	if p.textTabActive() {
		return fmt.Sprintf("%s | %d rows", elapsed, len(set.Rows))
	}
	row, col := 0, 0
	if len(set.Rows) > 0 {
		r, c := p.results.SelectedCell()
		row, col = r+1, c+1
	}
	return fmt.Sprintf("%s | Row: %d, Col: %d | %d rows", elapsed, row, col, len(set.Rows))
}

// formatElapsedHMS renders d as SSMS's "H:MM:SS" query-execution duration.
func formatElapsedHMS(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	sec := d / time.Second
	return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
}
