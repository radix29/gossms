package tui

import (
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// log_viewer.go is SSMS's Log File Viewer: one panel per server showing one
// error log file, or several of one family merged into one date-sorted grid.
// This file is the panel's state, construction and layout; the toolbar is in
// log_viewer_toolbar.go, the read in log_viewer_load.go, the grid's rows in
// log_viewer_rows.go and the file selection and export in log_viewer_files.go.
// Drawing is in log_viewer_draw.go and input in log_viewer_input.go.

// logReadTimeout bounds one enumeration or one read. The current log on a busy
// instance runs to tens of thousands of lines and xp_readerrorlog parses it
// server-side, so this is generous — but a panel that never comes back is worse
// than one that says it gave up.
const logReadTimeout = 60 * time.Second

// logFilterLabel and logFilterWidth size the toolbar's filter field, the only
// editable control on the row: it takes whatever the selectors and buttons
// leave, down to this floor.
const (
	logFilterLabel = "Filter:"
	logFilterWidth = 24
)

// logFileRef names one log file: the two arguments
// gosmo.Server.ReadLogFiltered takes. A LogViewer holds an ordered set
// of these rather than a single pair, so several files can be merged into one
// date-sorted grid — the single-file view is the one-element case.
type logFileRef struct {
	Type gosmo.ErrorLogType
	Num  int
}

// logFamilies are the log families the panel enumerates, offers and can merge
// across, in the order a mixed selection is merged and labelled in. The order
// is what breaks a timestamp tie between two families deterministically — see
// ShowLogs and sortLogRowsDesc.
var logFamilies = []gosmo.ErrorLogType{gosmo.ErrorLogSQLServer, gosmo.ErrorLogAgent}

// logFamilyShortName names a family for a label that already carries other
// text. gosmo spells the Agent family "SQL Server Agent", which is right on its
// own and repeats the instance's name in every row of a merged grid.
func logFamilyShortName(t gosmo.ErrorLogType) string {
	if t == gosmo.ErrorLogAgent {
		return "Agent"
	}
	return t.String()
}

// logRow is one entry together with the file it was read from. The file is not
// recoverable from the entry — two archives' rows are the same type — and a
// merged grid has to name it, both in the File column and in the details pane.
type logRow struct {
	entry *gosmo.ErrorLogEntry
	ref   logFileRef
}

// logFileError is one selected file that could not be read. A merged read
// reports these beside whatever did come back rather than failing the panel:
// one unreadable archive must not empty a grid holding three good files.
type logFileError struct {
	ref logFileRef
	err error
}

// LogViewer is the Log File Viewer panel: a grid of one or more log files'
// entries over a details pane showing the selected entry in full, with the log
// family and the file selection chosen from the toolbar.
//
// Reads run on the panel's host connection rather than one of its own — each is
// a one-shot query bounded by logReadTimeout with nothing on a timer, so no
// background traffic queues behind the shared connection.
type LogViewer struct {
	app  *App
	conn *db.ServerConn

	rect   core.Rect
	active bool

	// logType is the family the two selectors address: the one whose files the
	// file selector lists and the one Recycle acts on. It is *not* a constraint
	// on sel, which may span both families — it is what the panel means by "the
	// family on screen" for the actions that can only mean one. A selection
	// entirely of one family sets it; a mixed one leaves it where it was.
	// Switching it always lands on that family's current log.
	logType gosmo.ErrorLogType

	// sel is the ordered set of files on screen, never empty, and not
	// necessarily of one family. Its order is the order the reads are merged
	// in, which is what breaks a timestamp tie deterministically — see
	// sortLogRowsDesc.
	sel []logFileRef

	// pending is the file checklist's working copy, edited by its toggles and
	// applied as a set. A checklist that wrote straight into sel would leave
	// the grid describing files it had not read the moment the menu was
	// dismissed with Escape.
	pending []logFileRef

	// readErrs are the selected files the last read could not fetch, kept so
	// the status line can say how much of the selection the grid is showing.
	readErrs []logFileError

	// files caches each family's enumeration, so opening the file selector
	// doesn't re-run sp_enumerrorlogs on every click. Refresh drops it.
	files map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile

	// search is the server-side narrowing in force — xp_readerrorlog's own
	// search strings and date range, edited by the Search dialog; zero means the
	// whole file. Distinct from the filter field below, which narrows what was
	// already read. See log_search_dialog.go.
	search gosmo.LogSearch

	// entries is everything the last read returned, merged across the selected
	// files and sorted newest first; shown is the filtered subset the grid is
	// built from. entries is kept whole so clearing the filter needs no second
	// read.
	entries []logRow
	shown   []logRow

	grid     *controls.DataGrid
	filter   *widgets.InputField
	splitter *layout.Splitter

	// filterFocused sends keys to the filter field instead of the grid. Tab
	// toggles it, a click in either claims it — like QueryPanel's
	// resultsFocused.
	filterFocused bool

	toolRect   core.Rect
	gridRect   core.Rect
	detailRect core.Rect

	tools []toolButton
	// toolsEnd is the column just past the last laid-out cell, where the
	// filter field starts — mirrors ActivityMonitor.toolsEnd.
	toolsEnd int

	// more is the "More ▾" cell standing in for the buttons this row was too
	// narrow to draw, and hidden the indexes it holds. The row wants 121
	// columns once both selectors carry a real file label, and the pane gets
	// 70% of the terminal — so Export and Recycle were unreachable on any
	// ordinary terminal, neither having a key binding.
	more   toolButton
	hidden []int

	// detailScroll is the first drawn line of the details pane, so a long
	// message can be read past the pane's height without resizing it.
	detailScroll int

	// detailCache is the last detailLines result, valid for the entry and width
	// it was built at. The wrap is the panel's only per-frame allocation of any
	// size — a stack dump entry wraps to hundreds of lines, and the draw and
	// every scroll step ask for all of it. invalidateDetailCache clears it where
	// the text changes without the entry pointer changing.
	detailCache      []string
	detailCacheEntry *gosmo.ErrorLogEntry
	detailCacheWidth int

	// read guards the in-flight file read: a superseded result applies only if
	// it is still the most recent, and Close cancels it so a panel closed
	// mid-read doesn't leave the query running. See latest.
	read latest
	busy bool

	dragZone logDragZone
}

// logDragZone names the LogViewer sub-region that owns the in-progress mouse
// gesture — see QueryPanel.dragZone for why one is needed at all.
type logDragZone int

const (
	lZoneNone logDragZone = iota // no gesture in progress
	lZoneSplitter
	lZoneGrid
	lZoneFilter
	lZoneToolbar
	// lZoneUnclaimed is a press no sub-region wanted. It still owns the gesture,
	// so the repeats tcell sends while the button is held are swallowed instead
	// of landing on whatever the pointer drifts over.
	lZoneUnclaimed
)

// NewLogViewer creates the panel for one server connection, pointed at the
// given log file. Nothing is read until Load runs.
func NewLogViewer(app *App, sc *db.ServerConn, logType gosmo.ErrorLogType, logNum int) *LogViewer {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	// Message takes whatever Date and Source leave rather than being sized to
	// its own longest entry.
	grid.SetFillLastColumn(true)
	grid.SetStatusStyle(resultsStatusStyle)
	grid.OnCopyRequest = app.copyWithStatus
	grid.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	lv := new(LogViewer{
		app:      app,
		conn:     sc,
		logType:  logType,
		sel:      []logFileRef{{Type: logType, Num: logNum}},
		files:    make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile),
		grid:     grid,
		filter:   widgets.NewInputField(logFilterLabel, logFilterWidth, false),
		splitter: layout.NewHorizontalSplitter("─── Selected row details ─── (drag or Ctrl+Up/Down to resize)"),
	})
	lv.splitter.SetRatio(0.7)
	lv.buildTools()
	return lv
}

// Title returns the panel's tab title (Panel interface).
func (lv *LogViewer) Title() string { return "Logs — " + lv.conn.Opts.Server }

// SetActive marks this panel focused (Activatable interface).
func (lv *LogViewer) SetActive(v bool) {
	lv.active = v
	lv.grid.Focus(v && !lv.filterFocused)
	lv.filter.Focus(v && lv.filterFocused)
	lv.splitter.SetActive(v)
}

// Close cancels any in-flight read. Called from App.closePanelAt; the
// connection belongs to App, so there is nothing else to release. The token
// already discards a superseded result, but without the cancel the query runs
// on the shared host connection until logReadTimeout.
func (lv *LogViewer) Close() { lv.read.Cancel() }

// SetBounds positions the panel: the toolbar row, then the grid and the
// details pane on either side of the splitter.
func (lv *LogViewer) SetBounds(x, y, w, h int) {
	lv.rect = core.Rect{X: x, Y: y, W: w, H: h}
	if h >= 1 {
		lv.toolRect = core.Rect{X: x, Y: y, W: w, H: 1}
	} else {
		lv.toolRect = core.Rect{}
	}
	lv.layoutTools()
	lv.splitter.SetBounds(x, y+1, w, h-1)
	lv.layoutChildren()
}

// layoutChildren gives the grid and the details pane their halves of the area
// below the toolbar, on every resize and after every splitter drag.
func (lv *LogViewer) layoutChildren() {
	lv.gridRect = lv.splitter.FirstRect()
	lv.detailRect = lv.splitter.SecondRect()
	lv.grid.SetBounds(lv.gridRect.X, lv.gridRect.Y, lv.gridRect.W, lv.gridRect.H)
}

// layoutTools places the toolbar cells, collapsing whatever does not fit into
// the "More ▾" menu (see layoutToolButtonsOverflow), then the filter field in
// whatever is left.
func (lv *LogViewer) layoutTools() {
	var x int
	lv.hidden, x = layoutToolButtonsOverflow(lv.tools, lv.toolRect, "", &lv.more)
	lv.toolsEnd = x
	// The field's own width excludes its label and brackets, so the fit test
	// adds them back — see widgets.InputField.Draw.
	need := core.DisplayWidth(logFilterLabel) + 1 + logFilterWidth + 2
	if lv.toolRect.W > 0 && x+need <= lv.toolRect.Right() {
		lv.filter.SetBounds(x, lv.toolRect.Y)
		return
	}
	lv.filter.SetBounds(-1, -1)
	// A field parked off-screen must not keep focus: HandleKey routes on
	// filterFocused alone, so keystrokes would go into a field that isn't drawn.
	// Narrowing the terminal mid-typing is enough to hit it.
	if lv.filterFocused {
		lv.setFilterFocused(false)
	}
}

// filterVisible reports whether the filter field found room on the toolbar.
// A field laid out off-screen must not take focus or draw.
func (lv *LogViewer) filterVisible() bool { return lv.filter.RectX() >= 0 }
