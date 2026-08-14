package tui

import (
	"context"
	"path/filepath"
	"time"

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

	// fileEnc and fileCRLF are how the opened file was encoded on disk, so
	// Save writes it back in the same shape rather than converting it to
	// LF-separated UTF-8. Their zero values are what a panel with no file
	// wants anyway. See decodeTextFile.
	fileEnc  fileEncoding
	fileCRLF bool

	// runMode is the resultsMode the in-flight (or most recent) execution
	// was started under, snapshotted by runQuery. Every decision that has
	// to agree with how that execution actually ran reads this, not
	// resultsMode: the Query menu can switch modes while the save dialog is
	// open or the query is running.
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
	// fresh allocation on every one (see sqlparse.FlattenLinesInto). Valid
	// only for the duration of one sqlCompletionCandidates call.
	completionBuf []rune

	executing bool
	cancel    context.CancelFunc

	// execDone is closed when the in-flight run's goroutine exits, which is
	// how tickExecuting knows to stop. Both execute paths close it from a
	// defer, so a panic can't leave the ticker waking the event loop once a
	// second for the rest of the process's life.
	execDone chan struct{}
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
	// An XML or JSON cell goes to its own query tab with the matching
	// highlighting instead of the grid's 60-column popup; anything else falls
	// through to the popup. The declared column type comes from the active
	// result set, since a value's own text isn't a reliable XML tell — see
	// classifyCellKind.
	results.OnShowValue = func(col int, column, value string) bool {
		return app.openCellValuePanel(p.columnType(col), column, value)
	}
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

// columnType returns the declared SQL Server type of the active result set's
// col'th column, or "" when there's no such column or the set carries no
// types.
func (p *QueryPanel) columnType(col int) string {
	rs, ok := p.activeResultSet()
	if !ok || col < 0 || col >= len(rs.ColumnTypes) {
		return ""
	}
	return rs.ColumnTypes[col]
}
