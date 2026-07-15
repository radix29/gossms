package tui

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// resultsStatusStyle is the results grid's status bar look — a light
// yellow background with black text, matching SQL Server Management
// Studio's query execution status bar. Shared with the toolbar's hover
// tooltip via theme.StyleGridStatus.
var resultsStatusStyle = theme.StyleGridStatus()

// ResultsMode selects how QueryPanel renders a successful result set —
// matching SSMS's Query > Results To Grid/Text/File.
type ResultsMode int

const (
	ResultsModeGrid ResultsMode = iota
	ResultsModeText
	ResultsModeFile
)

// QueryPanel holds a SQL editor on top and a results area below, separated
// by a draggable/keyboard-resizable horizontal Splitter. Once a query has
// run, the results area grows a tab bar: one tab per result set plus a
// Messages tab (PRINT output, rows-affected counts, errors). It implements
// the tuikit/layout.Panel interface so it can be hosted by a
// layout.PanelManager.
type QueryPanel struct {
	rect        core.Rect
	title       string
	editor      *controls.Editor
	results     *controls.DataGrid
	messages    *controls.Editor // read-only; backs the Messages tab (see onMessagesTab)
	resultsText *controls.Editor // read-only; backs Results To Text (see renderActiveTab, textTabActive)
	splitter    *layout.Splitter
	active      bool
	conn        *db.ServerConn // nil = none; may outlive a disconnect
	database    string         // "" = connection default database
	app         *App

	filePath    string      // last path used by Save; "" if never saved
	savedText   string      // editor text as of the last save/load; compared by Dirty
	resultsMode ResultsMode // Grid/Text/File — set via Query menu

	result    *query.Result // last execution's result; nil until first run
	activeTab int           // 0..len(result.Sets)-1 = result grids; len(result.Sets) = Messages
	tabRect   core.Rect     // results tab bar row; zero rect while hidden

	// execStart marks when the in-flight execution began — read by
	// updateResultsStatus for the live "elapsed" timer while executing is
	// true, and by tickExecuting to know when to stop waking the event loop.
	execStart time.Time

	// resultsNotice is a one-shot message (e.g. "No query to execute",
	// "Not connected") that takes priority over the computed elapsed/row/
	// col status in updateResultsStatus until the next real execution
	// starts (see runQuery) — without it, the very next Draw (which always
	// runs right after the event that set this) would immediately
	// recompute and overwrite it from the last real result before the user
	// ever saw it.
	resultsNotice string

	// messageErrorLines marks which rendered line of p.messages belongs to
	// an error message (query.Message.IsError) — built in renderActiveTab
	// alongside the text itself, one entry per line index so it stays in
	// sync with a message's text spanning more than one line. Read by
	// messagesHighlighter.
	messageErrorLines []bool

	// resultsFocused tracks which of the two sub-regions keyboard input
	// goes to: false (the default) is the editor, true is the results
	// grid. Set by whichever one a click last landed in — see HandleMouse.
	// Gates the splitter's Ctrl+Up/Down resize shortcut (see HandleKey) so
	// it only fires while the results grid holds focus, the same way
	// App.handleKey gates the explorer/panels splitter to explorer focus —
	// otherwise it steals Ctrl+Up/Down from the editor on every keystroke.
	resultsFocused bool

	executing bool
	cancel    context.CancelFunc
}

// NewQueryPanel creates a new query panel bound to the given App (for
// connection lookup and status updates) and titled accordingly.
func NewQueryPanel(app *App, title string) *QueryPanel {
	results := controls.NewDataGrid()
	results.SetCellCursor(true)
	results.SetRowNumbers(true)
	results.SetStatusStyle(resultsStatusStyle)
	results.OnCopyRequest = func(text string) {
		app.writeClipboard(text)
		app.setStatus("Copied to clipboard")
	}
	p := new(QueryPanel{
		app:      app,
		title:    title,
		editor:   controls.NewEditor(controls.SQLHighlighter(theme.Active())),
		results:  results,
		splitter: layout.NewHorizontalSplitter("─── Results ─── (drag or Ctrl+Up/Down to resize)"),
	})
	p.messages = controls.NewEditor(p.messagesHighlighter)
	p.messages.SetReadOnly(true)
	p.resultsText = controls.NewEditor(nil)
	p.resultsText.SetReadOnly(true)
	p.editor.OnRightClick = func(x, y int) { app.showEditorContextMenu(x, y) }
	return p
}

// messagesHighlighter colors an entire line in the Messages tab red when it
// belongs to an error message (see query.Message.IsError and the parallel
// messageErrorLines slice built in renderActiveTab).
func (p *QueryPanel) messagesHighlighter(lines [][]rune, idx int) []controls.ColorRun {
	if idx >= len(p.messageErrorLines) || !p.messageErrorLines[idx] {
		return nil
	}
	pal := theme.Active()
	errStyle := tcell.StyleDefault.Background(pal.EditorBg).Foreground(pal.Error)
	return []controls.ColorRun{{Start: 0, Len: len(lines[idx]), Style: errStyle}}
}

// Title returns the panel's tab/window title. If the panel is associated
// with a file — opened via File > Open, or saved at least once via
// File > Save/Save As — the file's base name is shown instead of the
// original "Query N" (or "Script N") counter-based title.
func (p *QueryPanel) Title() string {
	if p.filePath != "" {
		return filepath.Base(p.filePath)
	}
	return p.title
}
func (p *QueryPanel) SetTitle(t string) { p.title = t }

// FilePath returns the path this panel was last saved to, or "" if never saved.
func (p *QueryPanel) FilePath() string { return p.filePath }

// Dirty reports whether the editor holds changes not yet saved to
// filePath (or, for a panel that's never been saved, any content at all) —
// satisfies layout.Dirty so the panel's tab bar can show a "*".
func (p *QueryPanel) Dirty() bool { return p.editor.Text() != p.savedText }

// SetResultsMode changes how results render; the active tab re-renders
// immediately so Grid/Text applies to the result already on screen.
func (p *QueryPanel) SetResultsMode(m ResultsMode) {
	p.resultsMode = m
	label := map[ResultsMode]string{
		ResultsModeGrid: "Results To Grid",
		ResultsModeText: "Results To Text",
		ResultsModeFile: "Results To File",
	}[m]
	p.renderActiveTab()
	p.app.setStatus(label + " selected for " + p.title)
}

// SetBounds positions the panel and lays out the editor/splitter/results.
func (p *QueryPanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	// Row 0 is the title bar; the splitter manages everything below it.
	p.splitter.SetBounds(x, y+1, w, h-1)
	p.layoutChildren()
}

func (p *QueryPanel) layoutChildren() {
	top := p.splitter.FirstRect()
	bottom := p.splitter.SecondRect()
	p.editor.SetBounds(top.X, top.Y, top.W, top.H)
	// Once a result exists, the first row of the results area is its tab bar.
	// results, messages, and resultsText share the same rect below it — only
	// one of the three is ever drawn/routed to at a time, see onMessagesTab
	// and textTabActive.
	if p.result != nil && bottom.H > 1 {
		p.tabRect = core.Rect{X: bottom.X, Y: bottom.Y, W: bottom.W, H: 1}
		p.results.SetBounds(bottom.X, bottom.Y+1, bottom.W, bottom.H-1)
		p.messages.SetBounds(bottom.X, bottom.Y+1, bottom.W, bottom.H-1)
		p.resultsText.SetBounds(bottom.X, bottom.Y+1, bottom.W, bottom.H-1)
	} else {
		p.tabRect = core.Rect{}
		p.results.SetBounds(bottom.X, bottom.Y, bottom.W, bottom.H)
		p.messages.SetBounds(bottom.X, bottom.Y, bottom.W, bottom.H)
		p.resultsText.SetBounds(bottom.X, bottom.Y, bottom.W, bottom.H)
	}
}

// onMessagesTab reports whether the active tab is Messages rather than a
// result-set grid — results and messages occupy the same rect (see
// layoutChildren), so exactly one of them is drawn, and routed keys/mouse,
// at any given time.
func (p *QueryPanel) onMessagesTab() bool {
	return p.result != nil && p.activeTab >= len(p.result.Sets)
}

// textTabActive reports whether the active tab is a result set being
// rendered as plain text (Query > Results To Text) rather than the grid —
// results, messages, and resultsText occupy the same rect (see
// layoutChildren), so exactly one of them is drawn, and routed keys/mouse,
// at any given time.
func (p *QueryPanel) textTabActive() bool {
	return !p.onMessagesTab() && p.resultsMode == ResultsModeText && p.result != nil
}

// SetActive marks this panel as focused.
func (p *QueryPanel) SetActive(v bool) {
	p.active = v
	p.syncFocusVisuals()
}

// editorHasFocus and resultsHasFocus report whether the editor / results-or-
// messages sub-region specifically holds real keyboard focus — both require
// the panel itself to be focused (p.active; see App.syncActivePanelFocus),
// not just visible, so switching tabs while Object Explorer has focus
// doesn't make the newly-shown panel appear focused too.
func (p *QueryPanel) editorHasFocus() bool  { return p.active && !p.resultsFocused }
func (p *QueryPanel) resultsHasFocus() bool { return p.active && p.resultsFocused }

// syncFocusVisuals applies editorHasFocus/resultsHasFocus to the editor's
// cursor visibility, the results grid's selection highlight, and the
// Messages editor's cursor visibility — called whenever p.active or
// p.resultsFocused changes (SetActive, setResultsFocused) so at most one of
// the two sub-regions ever shows itself as focused at a time.
func (p *QueryPanel) syncFocusVisuals() {
	p.editor.SetActive(p.editorHasFocus())
	p.results.Focus(p.resultsHasFocus())
	p.messages.SetActive(p.resultsHasFocus())
}

// Draw renders the title bar, editor, splitter, results tab bar, and grid.
func (p *QueryPanel) Draw(s tcell.Screen) {
	pal := theme.Active()
	titleStyle := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	if p.editorHasFocus() {
		titleStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
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
	case p.textTabActive():
		p.resultsText.Draw(s)
	default:
		p.results.Draw(s)
		p.results.DrawOverlay(s)
	}
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

// updateResultsStatus refreshes the results grid's status bar, recomputed
// on every Draw so it tracks row/column navigation and, while a query is
// executing, ticks live off execStart (see tickExecuting). resultsNotice,
// when set, takes priority — see its field doc.
func (p *QueryPanel) updateResultsStatus() {
	if p.resultsNotice != "" {
		p.results.SetStatus(p.resultsNotice)
		return
	}
	switch {
	case p.executing:
		p.results.SetStatus(formatElapsedHMS(time.Since(p.execStart)) + " | Executing...")
	case p.result != nil && !p.onMessagesTab() && !p.textTabActive():
		set := p.result.Sets[p.activeTab]
		row, col := 0, 0
		if len(set.Rows) > 0 {
			r, c := p.results.SelectedCell()
			row, col = r+1, c+1
		}
		p.results.SetStatus(fmt.Sprintf("%s | Row: %d, Col: %d | %d rows",
			formatElapsedHMS(p.result.Elapsed), row, col, len(set.Rows)))
	}
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

// drawTabBar renders the result-set/Messages tabs, styled like the
// PanelManager's panel tabs.
func (p *QueryPanel) drawTabBar(s tcell.Screen) {
	if p.tabRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, p.tabRect, ' ', barStyle)
	col := p.tabRect.X + 1
	for i, label := range p.resultTabs() {
		tabStyle := barStyle
		if i == p.activeTab {
			tabStyle = tcell.StyleDefault.Background(pal.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
		}
		text := " " + label + " "
		w := core.DisplayWidth(text)
		if col+w > p.tabRect.Right() {
			break
		}
		core.DrawText(s, col, p.tabRect.Y, tabStyle, text)
		col += w + 1
	}
}

// resultTabs returns the tab labels for the last result: one per result
// set ("Results" alone when there's only one), plus Messages.
func (p *QueryPanel) resultTabs() []string {
	if p.result == nil {
		return nil
	}
	tabs := make([]string, 0, len(p.result.Sets)+1)
	if len(p.result.Sets) == 1 {
		tabs = append(tabs, "Results")
	} else {
		for i := range p.result.Sets {
			tabs = append(tabs, fmt.Sprintf("Results %d", i+1))
		}
	}
	return append(tabs, "Messages")
}

// setActiveTab switches the results area to tab i, if it exists.
func (p *QueryPanel) setActiveTab(i int) {
	if i < 0 || i >= len(p.resultTabs()) || i == p.activeTab {
		return
	}
	p.activeTab = i
	p.renderActiveTab()
}

// tabAt returns the tab index at screen column mx on the tab bar, or -1.
// The segment walk mirrors drawTabBar exactly so hits line up with pixels.
func (p *QueryPanel) tabAt(mx int) int {
	col := p.tabRect.X + 1
	for i, label := range p.resultTabs() {
		w := core.DisplayWidth(" " + label + " ")
		if mx >= col && mx < col+w {
			return i
		}
		col += w + 1
	}
	return -1
}

// Execute runs the query against the connected server. If the editor has
// an active text selection, only the selected text is run; otherwise the
// full editor content is run. This is what both the Query > Execute menu
// item and F5 call.
func (p *QueryPanel) Execute() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runQuery(sel)
		return
	}
	p.runQuery(p.editor.Text())
}

// ExecuteSelection runs only the editor's selected text, doing nothing but
// setting a status message if there is no active selection — the
// toolbar's dedicated "Execute Selection" button, as distinct from
// Execute, which falls back to running the whole script.
func (p *QueryPanel) ExecuteSelection() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runQuery(sel)
		return
	}
	p.app.setStatus("No selection to execute")
}

// CancelExecution cancels the in-flight query, if one is running.
func (p *QueryPanel) CancelExecution() {
	if p.executing && p.cancel != nil {
		p.cancel()
		p.app.setStatus("Cancelling query...")
	} else {
		p.app.setStatus("No query is currently executing")
	}
}

// runQuery is the shared execution path for Execute. The heavy lifting —
// GO batch splitting, the USE database switch, result sets, and the
// message stream — lives in internal/query.
func (p *QueryPanel) runQuery(queryText string) {
	if queryText == "" {
		p.resultsNotice = "No query to execute"
		return
	}
	if !p.app.isConnected(p.conn) {
		p.resultsNotice = "Not connected — use File > Connect"
		p.results.SetData([]string{"Message"}, [][]string{{"No active connection"}})
		return
	}
	if p.executing {
		p.app.setStatus("A query is already executing in this panel")
		return
	}
	p.messages.SetText("") // clear stale messages from any previous run
	p.messageErrorLines = nil
	sc := p.conn
	// Results To File wants every row a query actually returns, not just
	// what the grid would show — captured now (like sc above) since
	// p.resultsMode can change via the Query menu while this goroutine runs.
	maxRows := p.app.cfg.MaxResultRows
	if p.resultsMode == ResultsModeFile {
		maxRows = 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus("Executing query...")

	done := make(chan struct{})
	go p.tickExecuting(done)

	go func() {
		res := query.Execute(ctx, sc.Server.DB(), p.database, queryText, maxRows)
		// cancelled must be read before cancel() — calling cancel sets
		// ctx.Err() itself, which would make this always true otherwise.
		cancelled := ctx.Err() != nil
		cancel() // release ctx's resources now that the query is done, whether or not CancelExecution ever ran
		close(done)
		p.app.postEvent(func() {
			p.executing = false
			p.cancel = nil
			p.setResult(res, cancelled)
		})
		p.app.wakeEventLoop()
	}()
}

// tickExecuting wakes the event loop once a second while a query runs, so
// updateResultsStatus's live elapsed-time counter visibly ticks instead of
// only updating once the query finishes. Exits as soon as done closes.
func (p *QueryPanel) tickExecuting(done chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			p.app.wakeEventLoop()
		}
	}
}

// setResult installs a finished execution: picks the initial tab (first
// grid, or Messages when there are no grids or the run had errors — same
// as SSMS), makes room for the tab bar, and renders.
func (p *QueryPanel) setResult(res *query.Result, cancelled bool) {
	// A mid-script "USE otherdb" changes the session's database out from
	// under p.database — res.Database (read off the same connection right
	// after the script ran; see query.Execute) is the source of truth from
	// here on, so the connection-info bar and the next Execute's own USE
	// stay in sync with it instead of the stale value from before this run.
	if res.Database != "" {
		p.database = res.Database
	}
	p.result = res
	p.activeTab = 0
	if len(res.Sets) == 0 || res.HasErrors() {
		p.activeTab = len(res.Sets) // Messages tab
	}
	p.layoutChildren()

	if p.resultsMode == ResultsModeFile && len(res.Sets) > 0 {
		p.promptWriteResults(res)
	}
	p.renderActiveTab()

	elapsed := res.Elapsed.Round(time.Millisecond)
	switch {
	case cancelled:
		p.app.setStatus("Query cancelled")
	case res.HasErrors():
		p.app.setStatus(fmt.Sprintf("Query completed with errors in %v — see Messages", elapsed))
	default:
		p.app.setStatus(fmt.Sprintf("Query completed in %v — %d row(s), %d message(s)",
			elapsed, res.TotalRows(), len(res.Messages)))
	}
}

// renderActiveTab loads the active tab's content into the results grid or
// resultsText editor, honouring the panel's Grid/Text results mode.
func (p *QueryPanel) renderActiveTab() {
	res := p.result
	if res == nil {
		return
	}
	// +2 to convert the Options dialog's "max cell length" (a character
	// count) into a column-width clamp, matching computeColWidths's own
	// header-width convention of content width + 1 column of padding on
	// each side.
	p.results.SetMaxCellWidth(p.app.cfg.MaxCellLength + 2)
	if p.onMessagesTab() {
		// A message's Text may itself span multiple lines (a detailed SQL
		// Server error, say) — split each one so messageErrorLines stays a
		// per-rendered-line slice in lockstep with what SetText below will
		// actually produce (Editor.SetText splits on "\n" the same way).
		var textLines []string
		var errLines []bool
		for _, m := range res.Messages {
			for _, l := range strings.Split(m.Text, "\n") {
				textLines = append(textLines, l)
				errLines = append(errLines, m.IsError)
			}
		}
		p.messageErrorLines = errLines
		p.messages.SetText(strings.Join(textLines, "\n"))
		return
	}
	set := res.Sets[p.activeTab]
	if p.resultsMode == ResultsModeText {
		p.resultsText.SetText(formatResultsAsText(set))
		return
	}
	p.results.SetData(set.Columns, set.Rows)
}

// formatResultsAsText renders set as SSMS's Results To Text look: a header
// row, a dashed separator, then one line per data row, each column padded
// to its widest value so columns visually line up like a real table.
func formatResultsAsText(set query.ResultSet) string {
	widths := make([]int, len(set.Columns))
	for i, c := range set.Columns {
		widths[i] = core.DisplayWidth(c)
	}
	for _, row := range set.Rows {
		for i, cell := range row {
			if w := core.DisplayWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var sb strings.Builder
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(core.PadRight(cell, widths[i]))
		}
		sb.WriteByte('\n')
	}
	writeRow(set.Columns)
	seps := make([]string, len(widths))
	for i, w := range widths {
		seps[i] = strings.Repeat("-", w)
	}
	writeRow(seps)
	for _, row := range set.Rows {
		writeRow(row)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// promptWriteResults implements Results To File: asks for a path, writes
// every result set as CSV, and reports the outcome as an extra message on
// the result (so it shows up in the Messages tab too).
func (p *QueryPanel) promptWriteResults(res *query.Result) {
	p.app.fileDialog.ShowSave("Results To File", "results.csv", func(path string) {
		n, err := writeCSV(path, res.Sets)
		msg := query.Message{Text: fmt.Sprintf("%d row(s) written to %s", n, path)}
		if err != nil {
			msg = query.Message{Text: fmt.Sprintf("write results: %v", err), IsError: true}
		}
		res.Messages = append(res.Messages, msg)
		if p.result == res {
			p.renderActiveTab()
		}
		p.app.setStatus(msg.Text)
	})
}

// HandleKey routes keys to result tab switching (Ctrl+PgUp/PgDn), F5
// execute, or whichever of the editor/results grid last got a mouse click
// (see resultsFocused). The splitter's Ctrl+Up/Down resize only gets first
// refusal while the results grid holds focus — while the editor holds it,
// Ctrl+arrows must reach the editor's own word-jump/line-select bindings
// instead of being swallowed for resize on every keystroke.
func (p *QueryPanel) HandleKey(ev *tcell.EventKey) bool {
	// The results grid's context menu or "Show Value" popup, if open, must
	// get every key unconditionally — both are centred on the whole screen
	// (see controls.DataGrid.DrawOverlay), independent of resultsFocused, so
	// without this check their keys (Shift+arrows, Ctrl+A, Escape) would
	// fall through to the editor instead whenever the editor holds focus.
	if !p.onMessagesTab() && !p.textTabActive() && p.results.OverlayActive() {
		return p.results.HandleKey(ev)
	}
	if ev.Key() == tcell.KeyF5 {
		p.Execute()
		return true
	}
	// Ctrl+Enter selects the T-SQL statement at the cursor (see
	// controls.Editor.SelectStatementAtCursor) — the first step toward
	// "execute current statement"; this only selects, it doesn't run it.
	// Like Ctrl+PgUp/PgDn just below, only a terminal with a modern
	// keyboard protocol reports Ctrl held on Enter — elsewhere this key
	// arrives indistinguishable from plain Enter and falls through to
	// inserting a newline instead.
	if ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModCtrl != 0 {
		p.editor.SelectStatementAtCursor()
		return true
	}
	// Ctrl+PgUp/PgDn cycle the result tabs. Like Ctrl+Tab (see app_events),
	// the Ctrl modifier on these keys is only reported by terminals with a
	// modern keyboard protocol; elsewhere they stay plain PgUp/PgDn and
	// fall through to the editor.
	if p.result != nil && ev.Modifiers()&tcell.ModCtrl != 0 {
		switch ev.Key() {
		case tcell.KeyPgUp:
			p.setActiveTab(p.activeTab - 1)
			return true
		case tcell.KeyPgDn:
			p.setActiveTab(p.activeTab + 1)
			return true
		}
	}
	if !p.resultsFocused {
		return p.editor.HandleKey(ev)
	}
	if p.splitter.HandleKey(ev) {
		p.layoutChildren()
		return true
	}
	switch {
	case p.onMessagesTab():
		if p.messages.HandleKey(ev) {
			return true
		}
	case p.textTabActive():
		if p.resultsText.HandleKey(ev) {
			return true
		}
	default:
		if p.results.HandleKey(ev) {
			return true
		}
	}
	if ev.Key() == tcell.KeyEscape {
		p.setResultsFocused(false)
		return true
	}
	return false
}

// setResultsFocused switches keyboard focus between the editor and results
// grid, updating both sub-regions' visual focus state to match (see
// syncFocusVisuals).
func (p *QueryPanel) setResultsFocused(v bool) {
	p.resultsFocused = v
	p.syncFocusVisuals()
}

// HandleMouse routes mouse events to the splitter (drag), result tabs,
// editor, results grid, or results-text view.
func (p *QueryPanel) HandleMouse(ev *tcell.EventMouse) bool {
	// Same reasoning as the OverlayActive check at the top of HandleKey:
	// the popup/menu can visually overlap the editor's rect, so it must get
	// every mouse event — including the drag-release that follows a
	// click-drag selection inside it — before any position-based routing
	// below gets a chance to hand the click to the editor instead.
	if !p.onMessagesTab() && !p.textTabActive() && p.results.OverlayActive() {
		p.setResultsFocused(true)
		return p.results.HandleMouse(ev)
	}
	mx, my := ev.Position()
	// Always forward release events — regardless of position — to the
	// splitter, the query editor, the messages view, the results-text view,
	// and the results grid, so an in-progress splitter drag, text-selection
	// drag, or cell-block selection drag terminates cleanly even if the
	// cursor has moved outside this panel's column (or out of whichever of
	// those widgets started the drag) before the button was released.
	// Without forwarding to results too, its own drag-tracking flag never
	// resets, so every click after the very first one in the grid's
	// lifetime gets mistaken for a continued drag from that first click's
	// anchor instead of a fresh single-cell selection.
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if p.splitter.HandleMouse(ev) {
			p.layoutChildren()
			handled = true
		}
		if p.editor.HandleMouse(ev) {
			handled = true
		}
		if p.messages.HandleMouse(ev) {
			handled = true
		}
		if p.resultsText.HandleMouse(ev) {
			handled = true
		}
		if p.results.HandleMouse(ev) {
			handled = true
		}
		return handled
	}
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if p.splitter.HandleMouse(ev) {
		p.layoutChildren()
		return true
	}
	if p.tabRect.H == 1 && my == p.tabRect.Y && ev.Buttons() == tcell.Button1 {
		if i := p.tabAt(mx); i >= 0 {
			p.setActiveTab(i)
		}
		return true
	}
	// A left- or right-click decides which sub-region owns keyboard focus
	// from now on (see resultsFocused) — matches ordinary GUI
	// click-to-focus, and is the only way focus moves into the results
	// grid (Escape is the way back out, see HandleKey).
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		if p.editor.HandleMouse(ev) {
			p.setResultsFocused(false)
			return true
		}
		switch {
		case p.onMessagesTab():
			if p.messages.HandleMouse(ev) {
				p.setResultsFocused(true)
				return true
			}
		case p.textTabActive():
			if p.resultsText.HandleMouse(ev) {
				p.setResultsFocused(true)
				return true
			}
		case p.results.HandleMouse(ev):
			p.setResultsFocused(true)
			return true
		}
		return false
	}
	if p.editor.HandleMouse(ev) {
		return true
	}
	switch {
	case p.onMessagesTab():
		return p.messages.HandleMouse(ev)
	case p.textTabActive():
		return p.resultsText.HandleMouse(ev)
	default:
		return p.results.HandleMouse(ev)
	}
}

// writeCSV writes every result set to path as CSV — a header row then data
// rows per set, sets separated by a blank line — returning the total number
// of data rows written. A failing Close (e.g. a disk-full flush error the OS
// only reports at close time) is reported too, not silently dropped, unless
// an earlier error already explains the failure.
func writeCSV(path string, sets []query.ResultSet) (n int, err error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	w := csv.NewWriter(f)
	for i, set := range sets {
		if i > 0 {
			w.Flush()
			if _, err = f.WriteString("\n"); err != nil {
				return n, err
			}
		}
		if err = w.Write(set.Columns); err != nil {
			return n, err
		}
		for _, row := range set.Rows {
			if err = w.Write(row); err != nil {
				return n, err
			}
			n++
		}
	}
	w.Flush()
	err = w.Error()
	return n, err
}
