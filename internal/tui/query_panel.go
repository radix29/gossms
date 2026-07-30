package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tui/planview"
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
	messages    *controls.Editor   // read-only; backs the Messages tab (see onMessagesTab)
	resultsText *controls.Editor   // read-only; backs Results To Text (see renderActiveTab, textTabActive)
	planView    *planview.PlanView // nil until the first Show Estimated/Actual Execution Plan; see planTabActive
	splitter    *layout.Splitter
	active      bool
	conn        *db.ServerConn // nil = none; may outlive a disconnect
	database    string         // "" = connection default database
	app         *App

	filePath    string      // last path used by Save; "" if never saved
	savedText   string      // editor text as of the last save/load; compared by Dirty
	resultsMode ResultsMode // Grid/Text/File — set via Query menu

	// runMode is the resultsMode the in-flight (or most recent) execution
	// was started under, snapshotted by runQuery. Every decision that has
	// to agree with the row cap that execution actually used reads this,
	// not resultsMode: the Query menu can switch modes while a query runs,
	// and a run started in Grid mode is capped to MaxResultRows, so
	// exporting its result as though it were a File-mode run would write a
	// truncated CSV with nothing to say so.
	runMode ResultsMode

	result    *query.Result // last execution's result; nil until first run
	activeTab int           // 0..len(result.Sets)-1 = result grids; len(result.Sets) = Messages
	tabRect   core.Rect     // results tab bar row; zero rect while hidden

	// statusRect is the results area's bottom row, where drawResultsStatus
	// paints the execution status for every tab the DataGrid isn't drawing
	// — the grid renders the same line itself, inside its own rect. Zero
	// when the results area is too short to spare a row.
	statusRect core.Rect

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

	// dragZone is the sub-region that claimed the Button1 press currently
	// being held, or qZoneNone between gestures. tcell resends Button1 on
	// every motion event while the button is down, and the results tab bar
	// sits one row below the splitter, which sits directly below the
	// editor — so a text-selection drag heading down out of the editor
	// walks over both. Without this it grabs the splitter (resizing the
	// panes mid-selection) and then flips the active results tab on every
	// motion event that lands on the tab row. Mirrors
	// propsheet.PropertySheet.dragZone; cleared on the release.
	dragZone queryDragZone

	// completionBuf is the flattened editor text sqlCompletionCandidates
	// scans, kept across keystrokes so a large script isn't re-copied into a
	// fresh allocation on every one (see flattenLinesInto). Valid only for the
	// duration of one sqlCompletionCandidates call.
	completionBuf []rune

	executing bool
	cancel    context.CancelFunc
}

// queryDragZone names the QueryPanel sub-region that owns the in-progress
// mouse gesture — see the dragZone field.
type queryDragZone int

const (
	qZoneNone queryDragZone = iota // no gesture in progress
	qZoneSplitter
	qZoneTabs
	qZoneEditor
	qZoneResults
	// qZoneUnclaimed is a press no sub-region wanted. It still owns the
	// gesture, so the repeats it generates are swallowed rather than
	// landing on whatever the pointer later drifts over.
	qZoneUnclaimed
)

// NewQueryPanel creates a new query panel bound to the given App (for
// connection lookup and status updates) and titled accordingly.
func NewQueryPanel(app *App, title string) *QueryPanel {
	results := controls.NewDataGrid()
	results.SetCellCursor(true)
	results.SetRowNumbers(true)
	results.SetStatusStyle(resultsStatusStyle)
	results.OnCopyRequest = app.copyWithStatus
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
	p.editor.SetCompletionProvider(p.newCompletionProvider())
	return p
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
	// Once a result or plan exists, the first row of the results area is its
	// tab bar. results, messages, resultsText, and planView share the same
	// rect below it — only one of the four is ever drawn/routed to at a
	// time, see onMessagesTab, textTabActive, and planTabActive.
	respY, respH := bottom.Y, bottom.H
	if (p.result != nil || p.planView != nil) && bottom.H > 1 {
		p.tabRect = core.Rect{X: bottom.X, Y: bottom.Y, W: bottom.W, H: 1}
		respY, respH = bottom.Y+1, bottom.H-1
	} else {
		p.tabRect = core.Rect{}
	}
	// DataGrid draws its own status bar on the last row of its rect; the
	// other three don't, so they're given one row less and drawResultsStatus
	// paints the same line into the gap. Sized here rather than per active
	// tab so switching tabs needs no relayout — the row lands in the same
	// place either way. Below two rows there's nothing left to give up, and
	// no status is drawn.
	p.statusRect = core.Rect{}
	otherH := respH
	if respH > 1 {
		otherH = respH - 1
		p.statusRect = core.Rect{X: bottom.X, Y: respY + respH - 1, W: bottom.W, H: 1}
	}
	p.results.SetBounds(bottom.X, respY, bottom.W, respH)
	p.messages.SetBounds(bottom.X, respY, bottom.W, otherH)
	p.resultsText.SetBounds(bottom.X, respY, bottom.W, otherH)
	if p.planView != nil {
		p.planView.SetBounds(bottom.X, respY, bottom.W, otherH)
	}
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
	if p.planView != nil {
		p.planView.SetActive(p.resultsHasFocus())
	}
}

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

// activeResultSet returns the result set the active tab shows, or ok false
// when the active tab isn't one (Messages, Execution Plan) or there's no
// result at all. Every caller indexing result.Sets by activeTab goes
// through here rather than subscripting directly — the two indices are kept
// in step by setResult/setActiveTab, but a mismatch would panic rather than
// merely misdraw.
func (p *QueryPanel) activeResultSet() (query.ResultSet, bool) {
	if p.result == nil || p.activeTab < 0 || p.activeTab >= len(p.result.Sets) {
		return query.ResultSet{}, false
	}
	return p.result.Sets[p.activeTab], true
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
	if !p.onMessagesTab() && !p.textTabActive() && !p.planTabActive() && p.results.OverlayActive() {
		return p.results.HandleKey(ev)
	}
	// Same rule for the execution plan's Operator Summary grid, which owns
	// the identical pair of popups one level down (see PlanView.OverlayActive).
	if p.planTabActive() && p.planView.OverlayActive() {
		return p.planView.HandleKey(ev)
	}
	// Same reasoning, for the SQL editor's own completion popup: it floats
	// over the editor's rect independently of resultsFocused, so it must
	// get every key before F5/Ctrl+PgUp/splitter routing below gets a
	// chance to misroute one meant for it.
	if p.editor.CompletionActive() {
		return p.editor.HandleKey(ev)
	}
	if ev.Key() == tcell.KeyF5 {
		p.Execute()
		return true
	}
	// Ctrl+R reloads this panel's autocomplete inventory — only while the
	// editor holds focus, matching Ctrl+Up/Down's identical resultsFocused
	// gating just below, so it doesn't collide with a future results-grid
	// binding on the same key.
	if ev.Key() == tcell.KeyCtrlR && !p.resultsFocused {
		p.refreshCompletionCache()
		return true
	}
	// Ctrl+Enter selects the T-SQL statement at the cursor (see
	// controls.Editor.SelectStatementAtCursor) — the first step toward
	// "execute current statement"; this only selects, it doesn't run it.
	//
	// Two encodings reach us, and both have to be accepted. A terminal with
	// a modern keyboard protocol reports it as Enter with Ctrl held. Every
	// other terminal — xfce4-terminal and the rest of the VTE family
	// included — sends a bare LF (0x0A) instead, which tcell decodes as
	// KeyCtrlJ, since plain Enter is CR (0x0D). Only the first was handled,
	// so on an ordinary terminal the key did nothing at all: KeyCtrlJ isn't
	// KeyEnter, so the editor below didn't want it either. Menu > Query >
	// Execute at Cursor kept working throughout, which is the tell that the
	// action was fine and only the binding was missing.
	if isCtrlEnter(ev) {
		p.editor.SelectStatementAtCursor()
		return true
	}
	// Ctrl+PgUp/PgDn cycle the result tabs. Like Ctrl+Tab (see app_events),
	// the Ctrl modifier on these keys is only reported by terminals with a
	// modern keyboard protocol; elsewhere they stay plain PgUp/PgDn and
	// fall through to the editor.
	if (p.result != nil || p.planView != nil) && ev.Modifiers()&tcell.ModCtrl != 0 {
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
	case p.planTabActive():
		if p.planView.HandleKey(ev) {
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
	if !p.onMessagesTab() && !p.textTabActive() && !p.planTabActive() && p.results.OverlayActive() {
		p.setResultsFocused(true)
		return p.results.HandleMouse(ev)
	}
	// Same rule for the execution plan's Operator Summary grid. PlanView's
	// own HandleMouse lets releases through to its inner branches, so the
	// forwarding below still clears any latch this doesn't reach.
	if p.planTabActive() && p.planView.OverlayActive() {
		p.setResultsFocused(true)
		return p.planView.HandleMouse(ev)
	}
	// Same reasoning, for the SQL editor's own completion popup.
	if p.editor.CompletionActive() {
		return p.editor.HandleMouse(ev)
	}
	mx, my := ev.Position()
	// Always forward release events — regardless of position — to the
	// splitter, the query editor, the messages view, the results-text view,
	// the execution plan view, and the results grid, so an in-progress
	// splitter drag, text-selection drag, or cell-block selection drag
	// terminates cleanly even if the cursor has moved outside this panel's
	// column (or out of whichever of those widgets started the drag) before
	// the button was released. Without forwarding to results too, its own
	// drag-tracking flag never resets, so every click after the very first
	// one in the grid's lifetime gets mistaken for a continued drag from
	// that first click's anchor instead of a fresh single-cell selection.
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
		if p.planView != nil && p.planView.HandleMouse(ev) {
			handled = true
		}
		p.dragZone = qZoneNone
		return handled
	}
	// Everything from the press that armed dragZone through to its release
	// belongs to the sub-region that claimed it, wherever the pointer has
	// drifted to since — including out of the panel's own columns, which is
	// why this outranks the bounds check. See the field's doc comment.
	//
	// A wheel tick arriving mid-gesture is swallowed rather than routed, same
	// rule as App.handleMouse's gestureOwner and PropertySheet's dragZone: it
	// isn't part of the gesture, and the positional routing below would hand
	// it to whichever sub-region the pointer has drifted over. App's own
	// gestureOwner (armed as ownerPanels for any press in this column) happens
	// to swallow it one level up today, so this is belt-and-braces — but the
	// invariant belongs to whoever owns the gesture, not to a caller that
	// might stop arming one.
	if p.dragZone != qZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return p.routeDrag(ev)
		}
		return true
	}
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if p.splitter.HandleMouse(ev) {
		p.layoutChildren()
		p.armDrag(ev, qZoneSplitter)
		return true
	}
	if p.tabRect.H == 1 && my == p.tabRect.Y && ev.Buttons() == tcell.Button1 {
		p.armDrag(ev, qZoneTabs)
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
			p.armDrag(ev, qZoneEditor)
			p.setResultsFocused(false)
			return true
		}
		if p.resultsHandleMouse(ev) {
			p.armDrag(ev, qZoneResults)
			p.setResultsFocused(true)
			return true
		}
		p.armDrag(ev, qZoneUnclaimed)
		return false
	}
	if p.editor.HandleMouse(ev) {
		return true
	}
	return p.resultsHandleMouse(ev)
}

// isCtrlEnter reports whether ev is Ctrl+Enter, in either encoding a
// terminal can deliver it as. A modern keyboard protocol (kitty, xterm's
// modifyOtherKeys) reports Enter with ModCtrl set; everything else sends a
// bare LF, 0x0A, which tcell decodes as KeyCtrlJ — plain Enter being CR,
// 0x0D. Nothing else in the app binds Ctrl+J, so accepting it costs
// nothing. See its use in HandleKey.
func isCtrlEnter(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyCtrlJ {
		return true
	}
	return ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModCtrl != 0
}

// resultsHandleMouse dispatches to whichever of the four widgets sharing
// the results rect is currently shown — see layoutChildren.
func (p *QueryPanel) resultsHandleMouse(ev *tcell.EventMouse) bool {
	switch {
	case p.onMessagesTab():
		return p.messages.HandleMouse(ev)
	case p.planTabActive():
		return p.planView.HandleMouse(ev)
	case p.textTabActive():
		return p.resultsText.HandleMouse(ev)
	default:
		return p.results.HandleMouse(ev)
	}
}

// armDrag records that zone consumed a Button1 press, so every further
// event until the release goes back to it — see the dragZone field.
func (p *QueryPanel) armDrag(ev *tcell.EventMouse, zone queryDragZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the sub-region that armed the
// gesture. qZoneTabs and qZoneUnclaimed swallow it: the tab switch already
// happened on the press, and there is nothing further to deliver — the
// point is only that no other sub-region sees the repeats.
func (p *QueryPanel) routeDrag(ev *tcell.EventMouse) bool {
	switch p.dragZone {
	case qZoneSplitter:
		if p.splitter.HandleMouse(ev) {
			p.layoutChildren()
		}
	case qZoneEditor:
		p.editor.HandleMouse(ev)
	case qZoneResults:
		p.resultsHandleMouse(ev)
	}
	return true
}
