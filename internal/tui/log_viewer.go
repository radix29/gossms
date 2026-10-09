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
// This file holds state, construction and layout; see log_viewer_toolbar.go,
// _load.go (the read), _rows.go (grid rows), _files.go (file selection and
// export), _draw.go and _input.go.

// logReadTimeout bounds one enumeration or one read. xp_readerrorlog parses
// the log server-side and a busy instance's current log runs to tens of
// thousands of lines, so this is generous.
const logReadTimeout = 60 * time.Second

// logFilterLabel and logFilterWidth size the toolbar's filter field, the only
// editable control on the row: it takes whatever the selectors and buttons
// leave, down to this floor.
const (
	logFilterLabel = "Filter:"
	logFilterWidth = 24
)

// logFileRef names one log file: the two arguments gosmo.Server.ReadLogFiltered
// takes. A LogViewer holds an ordered set of these so several files can merge
// into one grid; the single-file view is the one-element case.
type logFileRef struct {
	Type gosmo.ErrorLogType
	Num  int
}

// logFamilies are the log families the panel enumerates, offers and can merge
// across, in the order a mixed selection is merged and labelled in. The order
// breaks a timestamp tie between two families deterministically (ShowLogs,
// sortLogRowsDesc).
//
// The Database Mail log is gosmo's own family, read from
// msdb.dbo.sysmail_event_log rather than a file: one "file", number 0, which
// cannot be cycled (the Recycle cell purges it instead, deleteMailLog). A login
// without msdb access fails its enumeration, which leaves it out of the
// checklist as an instance without an Agent is left out.
var logFamilies = []gosmo.ErrorLogType{gosmo.ErrorLogSQLServer, gosmo.ErrorLogAgent, gosmo.ErrorLogDatabaseMail}

// logFamilyShortName names a family for a label that already carries other
// text. gosmo spells the Agent family "SQL Server Agent", which would repeat
// the instance's name in every row of a merged grid.
func logFamilyShortName(t gosmo.ErrorLogType) string {
	switch t {
	case gosmo.ErrorLogAgent:
		return "Agent"
	case gosmo.ErrorLogDatabaseMail:
		return "Mail"
	}
	return t.String()
}

// logRow is one entry together with the file it was read from. The file is not
// recoverable from the entry, and a merged grid has to name it (File column,
// details pane).
type logRow struct {
	entry *gosmo.ErrorLogEntry
	ref   logFileRef
}

// logFileError is one selected file that could not be read. A merged read
// reports these beside what did come back: one unreadable archive must not
// empty a grid holding three good files.
type logFileError struct {
	ref logFileRef
	err error
}

// LogViewer is the Log File Viewer panel: a grid of one or more log files'
// entries over a details pane showing the selected entry in full.
//
// Reads run on the panel's host connection rather than one of its own: each is
// a one-shot query bounded by logReadTimeout, so no background traffic queues
// behind the shared connection.
type LogViewer struct {
	app  *App
	conn *db.ServerConn

	rect   core.Rect
	active bool

	// logType is the family the two selectors address: the one whose files the
	// file selector lists and the one Recycle acts on. It is *not* a constraint on
	// sel, which may span families. A selection entirely of one family sets it; a
	// mixed one leaves it. Switching it always lands on that family's current log.
	logType gosmo.ErrorLogType

	// sel is the ordered set of files on screen, never empty, not necessarily of
	// one family. Its order is the merge order, which breaks timestamp ties
	// deterministically (sortLogRowsDesc).
	sel []logFileRef

	// pending is the file checklist's working copy, applied as a set. Writing
	// straight into sel would leave the grid describing unread files once the menu
	// was dismissed with Escape.
	pending []logFileRef

	// readErrs are the selected files the last read could not fetch, so the status
	// line can say how much of the selection the grid shows.
	readErrs []logFileError

	// files caches each family's enumeration so opening the file selector doesn't
	// re-run sp_enumerrorlogs on every click. Refresh drops it.
	files map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile

	// search is the server-side narrowing in force (xp_readerrorlog's search
	// strings and date range, edited by the Search dialog); zero means the whole
	// file. Distinct from the filter field, which narrows what was already read.
	search gosmo.LogSearch

	// entries is everything the last read returned, merged and sorted newest
	// first; shown is the filtered subset the grid is built from. entries is kept
	// whole so clearing the filter needs no second read.
	entries []logRow
	shown   []logRow

	// mailOwnOnly is the last read's answer to whether this login sees only its
	// own mail items' Database Mail entries (gosmo.Server.MailVisibility), said in
	// the status line and the Delete warning, since the purge is of every entry.
	// Asked only when the selection holds the Database Mail log.
	mailOwnOnly bool

	grid     *controls.DataGrid
	filter   *widgets.InputField
	splitter *layout.Splitter

	// filterFocused sends keys to the filter field instead of the grid. Tab
	// toggles it, a click in either claims it (like QueryPanel's resultsFocused).
	filterFocused bool

	toolRect   core.Rect
	gridRect   core.Rect
	detailRect core.Rect

	// tools is the toolbar row. Its More cell matters: the row wants 121 columns
	// once both selectors carry a real file label and the pane gets 70% of the
	// terminal, so without it Export and Recycle (no key bindings) are unreachable.
	tools controls.ToolRow
	// toolsEnd is the column just past the last laid-out cell, where the filter
	// field starts (cf. ActivityMonitor.toolsEnd).
	toolsEnd int

	// detailScroll is the first drawn line of the details pane, so a long message
	// can be read past the pane's height.
	detailScroll int

	// detailCache is the last detailLines result, valid for the entry and width it
	// was built at. The wrap is the panel's only sizeable per-frame allocation (a
	// stack dump wraps to hundreds of lines; draw and every scroll step want all
	// of it). invalidateDetailCache clears it where the text changes without the
	// entry pointer changing.
	detailCache      []string
	detailCacheEntry *gosmo.ErrorLogEntry
	detailCacheWidth int

	// read guards the in-flight file read: a superseded result applies only if
	// still the most recent, and Close cancels it so a closed panel doesn't leave
	// the query running. See latest.
	read latest
	busy bool

	dragZone logDragZone
}

// logDragZone names the LogViewer sub-region that owns the in-progress mouse
// gesture; see QueryPanel.dragZone.
type logDragZone int

const (
	lZoneNone logDragZone = iota // no gesture in progress
	lZoneSplitter
	lZoneGrid
	lZoneFilter
	lZoneToolbar
	// lZoneUnclaimed is a press no sub-region wanted. It still owns the gesture,
	// so tcell's repeats while the button is held are swallowed rather than landing
	// wherever the pointer drifts.
	lZoneUnclaimed
)

// NewLogViewer creates the panel for one server connection, pointed at the
// given log file. Nothing is read until Load runs.
func NewLogViewer(app *App, sc *db.ServerConn, logType gosmo.ErrorLogType, logNum int) *LogViewer {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	// Message takes whatever Date and Source leave rather than its own longest
	// entry.
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
// connection belongs to App. Without the cancel the query runs on the shared
// host connection until logReadTimeout.
func (lv *LogViewer) Close() { lv.read.Cancel() }

// SetBounds positions the panel: toolbar row, then grid and details pane
// either side of the splitter.
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

// layoutChildren gives the grid and details pane their halves of the area
// below the toolbar, on every resize and splitter drag.
func (lv *LogViewer) layoutChildren() {
	lv.gridRect = lv.splitter.FirstRect()
	lv.detailRect = lv.splitter.SecondRect()
	lv.grid.SetBounds(lv.gridRect.X, lv.gridRect.Y, lv.gridRect.W, lv.gridRect.H)
}

// layoutTools places the toolbar cells, collapsing what does not fit into the
// "More ▾" menu, then the filter field in what is left.
func (lv *LogViewer) layoutTools() {
	x := lv.tools.Layout(lv.toolRect, "")
	lv.toolsEnd = x
	// The field's width excludes its label and brackets, so the fit test adds them
	// back (widgets.InputField.Draw).
	need := core.DisplayWidth(logFilterLabel) + 1 + logFilterWidth + 2
	if lv.toolRect.W > 0 && x+need <= lv.toolRect.Right() {
		lv.filter.SetBounds(x, lv.toolRect.Y)
		return
	}
	lv.filter.SetBounds(-1, -1)
	// A field parked off-screen must not keep focus: HandleKey routes on
	// filterFocused alone, so keystrokes would go into an undrawn field. Narrowing
	// the terminal mid-typing hits it.
	if lv.filterFocused {
		lv.setFilterFocused(false)
	}
}

// filterVisible reports whether the filter field found room on the toolbar. A
// field laid out off-screen must not take focus or draw.
func (lv *LogViewer) filterVisible() bool { return lv.filter.RectX() >= 0 }
