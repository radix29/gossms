package tui

import (
	"fmt"
	"slices"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
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
	if !p.resultsShown {
		p.editor.DrawOverlay(s)
		return
	}
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
	// Drawn last, after the results grid's own overlay; see the "overlays drawn last"
	// rule in tuikit/README.md.
	p.editor.DrawOverlay(s)
}

// connInfoText builds the bar above the editor: "server | user (spid) | db", as
// SSMS's connection status bar: the one place in the panel that says what this
// query is actually running against, distinct from the tab bar's title. An open
// transaction is called out at the end: it holds locks, and closing the window
// rolls it back.
func (p *QueryPanel) connInfoText() string {
	if p.connectingTo != "" {
		if p.connectingDB != "" {
			return fmt.Sprintf("Connecting to %s | %s...", p.connectingTo, p.connectingDB)
		}
		return fmt.Sprintf("Connecting to %s...", p.connectingTo)
	}
	if p.conn == nil {
		return "(not connected)"
	}
	user := p.conn.Opts.User
	if user == "" {
		user = config.AuthMethodName(p.conn.Opts.AuthMethod)
	}
	if p.session != nil {
		user += fmt.Sprintf(" (%d)", p.session.SPID())
	}
	text := fmt.Sprintf("%s | %s | %s", p.conn.Opts.Server, user, p.database)
	if !p.connected() {
		return text + " (disconnected)"
	}
	if p.tranCount > 0 {
		text += fmt.Sprintf(" | %d open transaction%s", p.tranCount, pluralSuffix(p.tranCount))
	}
	return text
}

// notConnectedMessage is runQuery/runEstimatedPlan's resultsNotice when
// connected() is false, distinguishing a panel that was connected and had its
// connection silently dropped (p.conn still this panel's own *db.ServerConn, no
// longer open; see Reconnect) from one never connected, since Query > Reconnect
// has something to redial only in the former case. A connect still in flight is
// neither, and says so.
func (p *QueryPanel) notConnectedMessage() string {
	if p.connectingTo != "" {
		return fmt.Sprintf("Still connecting to %s — try again once it connects", p.connectingTo)
	}
	if p.conn != nil {
		return "Not connected — use Query > Reconnect"
	}
	return notConnectedMessage
}

// updateResultsStatus pushes the current status line into the results grid,
// recomputed on every Draw so it tracks row/column navigation and, while a query
// is executing, ticks live off execStart (the launch ticker repaints). The other
// three tabs get the same line from drawResultsStatus.
func (p *QueryPanel) updateResultsStatus() {
	p.results.SetStatus(p.resultsStatusText())
	p.results.SetStatusStyle(p.resultsStatusStyle())
}

// resultsStatusStyle is the status line's style: the usual one, or a bold alert
// while the line carries truncatedNotice, which must not read as a routine count.
func (p *QueryPanel) resultsStatusStyle() tcell.Style {
	if p.truncatedNotice() == "" {
		return resultsStatusStyle
	}
	pal := theme.Active()
	return tcell.StyleDefault.Background(pal.Error).Foreground(color.White).Bold(true)
}

// truncatedNotice is the status line's shout that Max fetch rows cut a result
// short: for the active set on a grid or text tab, for any set on the others. ""
// when nothing was cut, or the line shows something else (resultsNotice, a run
// in flight).
func (p *QueryPanel) truncatedNotice() string {
	if p.resultsNotice != "" || p.executing || p.result == nil {
		return ""
	}
	// The cap is a cut set's own length, not cfg's: Options may have changed it
	// since the run.
	cut := -1
	if set, ok := p.activeResultSet(); ok {
		if set.Truncated {
			cut = len(set.Rows)
		}
	} else if i := slices.IndexFunc(p.result.Sets, func(rs query.ResultSet) bool { return rs.Truncated }); i >= 0 {
		cut = len(p.result.Sets[i].Rows)
	} else if p.result.SinkSetsTruncated > 0 { // Results To File
		cut = p.result.MaxRows()
	}
	if cut < 0 {
		return ""
	}
	return fmt.Sprintf("MORE ROWS NOT FETCHED - MAX FETCH ROWS IS %d", cut)
}

// drawResultsStatus paints the status line for a tab the results grid isn't
// drawing (Messages, Results To Text, Execution Plan). The line lives on the
// DataGrid, which isn't on screen for any of them, so without this elapsed time,
// row counts and the live "Executing..." counter all vanished when the tab
// changed.
func (p *QueryPanel) drawResultsStatus(s tcell.Screen) {
	if p.statusRect.H != 1 {
		return
	}
	style := p.resultsStatusStyle()
	core.FillRect(s, p.statusRect, ' ', style)
	core.DrawTextRight(s, p.statusRect.X+1, p.statusRect.Y, p.statusRect.W-2,
		style, p.resultsStatusText())
}

// resultsStatusText builds the results area's status line for the active tab,
// led by truncatedNotice when a result was cut short. resultsNotice, when set,
// takes priority (see its field doc).
func (p *QueryPanel) resultsStatusText() string {
	text := p.statusDetailText()
	if n := p.truncatedNotice(); n != "" {
		return n + " | " + text
	}
	return text
}

// statusDetailText is resultsStatusText without truncatedNotice.
func (p *QueryPanel) statusDetailText() string {
	if p.resultsNotice != "" {
		return p.resultsNotice
	}
	if p.executing {
		text := formatHMS(time.Since(p.execStart)) + " | Executing..."
		// Only for a run that scans rows: an estimated plan leaves p.progress nil, where
		// a "0 rows" that never moves would be noise.
		if p.progress != nil {
			text += fmt.Sprintf(" | %d rows", p.progress.Rows())
		}
		return text
	}
	if p.result == nil {
		// Estimated plan: nothing ran, so there's no elapsed time or row count to report,
		// and the previous run's (left over from before setEstimatedPlan cleared
		// p.result) would be a lie.
		if p.planView != nil {
			return "Estimated execution plan"
		}
		return ""
	}
	elapsed := formatHMS(p.result.Elapsed)
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
		if p.textFormatting() {
			return fmt.Sprintf("%s | %d rows | Formatting as text...", elapsed, len(set.Rows))
		}
		return fmt.Sprintf("%s | %d rows", elapsed, len(set.Rows))
	}
	row, col := 0, 0
	if len(set.Rows) > 0 {
		r, c := p.results.SelectedCell()
		row, col = r+1, c+1
	}
	if p.sorting() {
		return fmt.Sprintf("%s | Row: %d, Col: %d | %d rows | Sorting...", elapsed, row, col, len(set.Rows))
	}
	return fmt.Sprintf("%s | Row: %d, Col: %d | %d rows", elapsed, row, col, len(set.Rows))
}
