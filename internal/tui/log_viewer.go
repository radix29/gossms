package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// log_viewer.go is SSMS's Log File Viewer: one panel per server showing one
// error log file at a time, either family. The two selectors, the filter and
// the grid live here; drawing is in log_viewer_draw.go and input in
// log_viewer_input.go.

// logReadTimeout bounds one enumeration or one read. The current log on a busy
// instance runs to tens of thousands of lines and xp_readerrorlog parses the
// file server-side, so this is generous — but a panel that never comes back is
// worse than one that says it gave up.
const logReadTimeout = 60 * time.Second

// logFilterLabel and logFilterWidth size the toolbar's filter field. It is
// the only editable control on the row, so it gets whatever the selectors
// and buttons leave, down to this floor.
const (
	logFilterLabel = "Filter:"
	logFilterWidth = 24
)

// LogViewer is the Log File Viewer panel: a grid of one log file's entries over
// a details pane showing the selected entry in full, with the log family and
// archive number chosen from the toolbar.
//
// Reads run on the panel's host connection rather than one of its own: each is
// a one-shot query bounded by logReadTimeout with nothing on a timer, so there
// is no background traffic for a shared connection to queue behind.
type LogViewer struct {
	app  *App
	conn *db.ServerConn

	rect   core.Rect
	active bool

	// logType and logNum are the file currently shown — the two arguments
	// gosmo.Server.ReadLogContext takes.
	logType gosmo.ErrorLogType
	logNum  int

	// files caches each family's enumeration, so opening the file selector
	// doesn't re-run sp_enumerrorlogs on every click. Refresh drops it.
	files map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile

	// entries is everything the last read returned, shown the filtered subset.
	// The grid is built from shown; entries is kept whole so clearing the
	// filter needs no second read.
	entries []*gosmo.ErrorLogEntry
	shown   []*gosmo.ErrorLogEntry

	grid     *controls.DataGrid
	filter   *widgets.InputField
	splitter *layout.Splitter

	// filterFocused sends keys to the filter field instead of the grid. Tab
	// toggles it; a click in either one claims it, like QueryPanel's
	// resultsFocused.
	filterFocused bool

	toolRect   core.Rect
	gridRect   core.Rect
	detailRect core.Rect

	tools []toolButton
	// toolsEnd is the column just past the last laid-out button, where the
	// filter field starts — mirrors ActivityMonitor.toolsEnd.
	toolsEnd int

	// detailScroll is the first drawn line of the details pane, so a long
	// message can be read past the pane's height without resizing it.
	detailScroll int

	// detailCache is the last detailLines result, valid for the entry and width
	// it was built at. The wrap is the panel's only per-frame allocation of any
	// size — a stack dump entry wraps to hundreds of lines, and both the draw
	// and every scroll step ask for the whole thing. invalidateDetailCache
	// clears it wherever the text changes without the entry pointer changing.
	detailCache      []string
	detailCacheEntry *gosmo.ErrorLogEntry
	detailCacheWidth int

	// seq guards against a superseded read landing after a newer one: every
	// load increments it and an async result is applied only if it is still
	// the most recent. Same role as DetailBrowser.seq.
	seq int
	// cancel aborts the in-flight read, called by Close so a panel closed
	// mid-read doesn't leave the query running.
	cancel context.CancelFunc
	busy   bool

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
	// lZoneUnclaimed is a press no sub-region wanted. It still owns the
	// gesture, so the repeats tcell sends while the button is held are
	// swallowed instead of landing on whatever the pointer drifts over.
	lZoneUnclaimed
)

// NewLogViewer creates the panel for one server connection, pointed at the
// given log file. Nothing is read until Load runs.
func NewLogViewer(app *App, sc *db.ServerConn, logType gosmo.ErrorLogType, logNum int) *LogViewer {
	grid := controls.NewDataGrid()
	grid.SetCellCursor(true)
	// Message is the column worth reading, so it takes whatever Date and
	// Source leave rather than being sized to its own longest entry.
	grid.SetFillLastColumn(true)
	grid.SetStatusStyle(resultsStatusStyle)
	grid.OnCopyRequest = app.copyWithStatus
	grid.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	lv := new(LogViewer{
		app:      app,
		conn:     sc,
		logType:  logType,
		logNum:   logNum,
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
// connection belongs to App, so there is nothing else to release.
func (lv *LogViewer) Close() { lv.cancelRead() }

// cancelRead aborts the in-flight read. Called when the panel closes and when
// a new read supersedes one: seq already discards a superseded result, but
// without cancelling, the query runs on the shared host connection until
// logReadTimeout.
func (lv *LogViewer) cancelRead() {
	if lv.cancel != nil {
		lv.cancel()
		lv.cancel = nil
	}
}

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

// layoutChildren gives the grid and the details pane their halves of the
// area below the toolbar. Called on every resize and after every splitter
// drag.
func (lv *LogViewer) layoutChildren() {
	lv.gridRect = lv.splitter.FirstRect()
	lv.detailRect = lv.splitter.SecondRect()
	lv.grid.SetBounds(lv.gridRect.X, lv.gridRect.Y, lv.gridRect.W, lv.gridRect.H)
}

// layoutTools places the toolbar cells (see layoutToolButtons), then the
// filter field in whatever is left.
func (lv *LogViewer) layoutTools() {
	x := layoutToolButtons(lv.tools, lv.toolRect, "")
	lv.toolsEnd = x
	// The field's own width excludes its label and brackets, which is why
	// the fit test adds them back — see widgets.InputField.Draw.
	need := core.DisplayWidth(logFilterLabel) + 1 + logFilterWidth + 2
	if lv.toolRect.W > 0 && x+need <= lv.toolRect.Right() {
		lv.filter.SetBounds(x, lv.toolRect.Y)
		return
	}
	lv.filter.SetBounds(-1, -1)
	// A field parked off-screen must not keep focus: HandleKey routes on
	// filterFocused alone, so keystrokes would go into a field that isn't
	// drawn. Narrowing the terminal mid-typing is enough to hit it.
	if lv.filterFocused {
		lv.setFilterFocused(false)
	}
}

// filterVisible reports whether the filter field found room on the toolbar.
// A field laid out off-screen must not take focus or draw.
func (lv *LogViewer) filterVisible() bool { return lv.filter.RectX() >= 0 }

// Toolbar cell indexes, in buildTools' layout order. Named because cells are
// addressed by index: popMenu anchors a selector's list under its own cell,
// and HandleKey runs the Refresh cell's action for F5.
const (
	logToolLogType = iota
	logToolFile
	logToolRefresh
	logToolExport
)

// buildTools defines the toolbar: the two selectors, then the buttons, in the
// order the logTool* constants name. Labels are rebuilt on every draw (see
// refreshToolLabels), since both selectors show what they currently point at.
func (lv *LogViewer) buildTools() {
	lv.tools = []toolButton{
		{action: lv.showLogTypeMenu},
		{action: lv.showLogFileMenu},
		{label: "Refresh", action: lv.Refresh},
		{label: "Export...", action: lv.export},
	}
	lv.refreshToolLabels()
}

// toolsEnabled reports whether the toolbar's actions are available — the one
// answer behind drawToolbar's dimming, the click path and F5, so a cell can
// never be drawn dimmed and still act on a click. It could, and clicking a
// dimmed Refresh mid-read started a second read while the first ran on.
func (lv *LogViewer) toolsEnabled() bool { return !lv.busy }

// runTool invokes toolbar cell i's action, or does nothing while the toolbar
// is disabled. Reports whether the action ran.
func (lv *LogViewer) runTool(i int) bool {
	if !lv.toolsEnabled() {
		return false
	}
	lv.tools[i].action()
	return true
}

// refreshToolLabels updates the two selectors' labels from the current
// selection.
func (lv *LogViewer) refreshToolLabels() {
	lv.tools[0].label = "Log: " + lv.logType.String() + " ▾"
	lv.tools[1].label = "File: " + lv.currentFileLabel() + " ▾"
}

// currentFileLabel names the file on screen, from the cached enumeration when
// there is one and from the archive number alone when there isn't — the
// selector is drawn before the first enumeration lands.
func (lv *LogViewer) currentFileLabel() string {
	for _, f := range lv.files[lv.logType] {
		if f.Number == lv.logNum {
			return errorLogFileLabel(f)
		}
	}
	if lv.logNum == 0 {
		return "Current"
	}
	return fmt.Sprintf("Archive #%d", lv.logNum)
}

// ShowLog points the panel at a log file and reads it. Reopening the panel
// from another tree node comes through here, so an already-open viewer
// switches files instead of a second one being created.
func (lv *LogViewer) ShowLog(logType gosmo.ErrorLogType, logNum int) {
	lv.logType, lv.logNum = logType, logNum
	lv.detailScroll = 0
	lv.Load()
}

// Refresh re-reads the current file and re-enumerates both families (F5 /
// toolbar). The enumeration is dropped rather than refreshed: a cycled log
// renumbers every archive, so only a fresh read can correct the cached list.
func (lv *LogViewer) Refresh() {
	lv.files = make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile)
	lv.Load()
}

// Load reads the current file in the background and applies the result on
// the UI goroutine. The enumeration of the same family rides along, so the
// file selector has its list without a second round trip.
func (lv *LogViewer) Load() {
	if !lv.app.isConnected(lv.conn) {
		lv.entries, lv.shown = nil, nil
		lv.grid.SetData(logGridColumns, nil)
		lv.setStatus("Not connected")
		return
	}
	lv.cancelRead()
	lv.seq++
	seq := lv.seq
	lv.busy = true
	lv.setStatus(fmt.Sprintf("Reading %s log %s...", lv.logType, lv.currentFileLabel()))
	lv.refreshToolLabels()

	logType, logNum := lv.logType, lv.logNum
	sc := lv.conn
	// One cancel for the panel to pull, but a fresh deadline per call: sharing
	// one logReadTimeout let a slow sp_enumerrorlogs eat the read's half of it,
	// timing out the file the user asked for because the *list* was slow.
	ctx, cancel := context.WithCancel(sc.Context())
	lv.cancel = cancel
	// safegoRepair, not safego: busy is cleared in the callback below, which a
	// panic on the read goroutine never reaches. toolsEnabled gates the whole
	// toolbar on it, so Refresh, Export and both selectors would sit dimmed and
	// inert until the panel was closed.
	lv.app.safegoRepair("reading an error log", func() { lv.readPanicked(seq) }, func() {
		defer cancel()
		enumCtx, enumCancel := context.WithTimeout(ctx, logReadTimeout)
		files, filesErr := sc.Server.EnumErrorLogsContext(enumCtx, logType)
		enumCancel()
		readCtx, readCancel := context.WithTimeout(ctx, logReadTimeout)
		defer readCancel()
		entries, err := sc.Server.ReadLogContext(readCtx, logType, logNum)
		lv.app.postAndWake(func() {
			if seq != lv.seq {
				return
			}
			lv.busy = false
			lv.cancel = nil
			if filesErr == nil {
				lv.files[logType] = files
			}
			lv.refreshToolLabels()
			if err != nil {
				lv.entries, lv.shown = nil, nil
				lv.grid.SetError(err)
				return
			}
			lv.entries = sortLogEntriesDesc(entries)
			lv.applyFilter() // resets detailScroll itself
		})
	})
}

// readPanicked releases the busy latch after a panic on the read goroutine —
// Load's safegoRepair step. Guarded by seq like the normal completion path: a
// newer Load has set busy for itself, and clearing it here would re-enable a
// toolbar whose read is still out.
func (lv *LogViewer) readPanicked(seq int) {
	if seq != lv.seq {
		return
	}
	lv.busy = false
	lv.cancel = nil
	lv.refreshToolLabels()
	lv.setStatus("Read stopped unexpectedly — see the log for details.")
}

// logGridColumns are the entry grid's columns. The marker on Date says which
// way rows are ordered; Source is whichever of ProcessInfo and ErrorLevel the
// log family populates.
var logGridColumns = []string{"Date ▼", "Source", "Message"}

// logExportColumns are the same columns without the sort marker — a header
// row in an exported file names the column, it doesn't describe the grid.
var logExportColumns = []string{"Date", "Source", "Message"}

// applyFilter rebuilds shown from entries and hands it to the grid, matching a
// case-insensitive substring over the source and message. An empty filter
// shows everything.
func (lv *LogViewer) applyFilter() {
	lv.invalidateDetailCache()
	needle := strings.ToLower(strings.TrimSpace(lv.filter.Value()))
	lv.shown = lv.shown[:0]
	for _, e := range lv.entries {
		if needle == "" || logEntryMatches(e, needle) {
			lv.shown = append(lv.shown, e)
		}
	}
	rows := make([][]string, 0, len(lv.shown))
	for _, e := range lv.shown {
		rows = append(rows, []string{formatSQLDate(e.Date), e.Source(), flattenLogText(e.Text)})
	}
	lv.grid.SetData(logGridColumns, rows)
	lv.detailScroll = 0
	lv.setStatus(lv.summary())
}

// invalidateDetailCache forces the next detailLines call to re-wrap. The cache
// is keyed on the entry pointer, but two of the three lines above the message
// name the log file rather than the entry — a fresh enumeration can rename
// "Archive #3" without the selected entry changing.
func (lv *LogViewer) invalidateDetailCache() {
	lv.detailCacheEntry, lv.detailCache = nil, nil
}

// summary is the status line under the grid: how much of the file is shown,
// and which file it is.
func (lv *LogViewer) summary() string {
	if len(lv.entries) == 0 {
		return fmt.Sprintf("%s log %s — no entries", lv.logType, lv.currentFileLabel())
	}
	if len(lv.shown) == len(lv.entries) {
		return fmt.Sprintf("%s log %s — %d entries", lv.logType, lv.currentFileLabel(), len(lv.entries))
	}
	return fmt.Sprintf("%s log %s — %d of %d entries match the filter",
		lv.logType, lv.currentFileLabel(), len(lv.shown), len(lv.entries))
}

// setStatus writes the panel's one-line state into the grid's own status
// bar, so it sits with the rows it describes.
func (lv *LogViewer) setStatus(s string) { lv.grid.SetStatus(s) }

// logEntryMatches reports whether needle (already lowercased) appears in
// the entry's source or message.
func logEntryMatches(e *gosmo.ErrorLogEntry, needle string) bool {
	return strings.Contains(strings.ToLower(e.Text), needle) ||
		strings.Contains(strings.ToLower(e.Source()), needle)
}

// flattenLogText makes one grid line out of a log entry's text. An entry can
// carry embedded newlines and tabs — the startup banner spans four lines — and
// a grid cell is one row tall, so they become spaces. The details pane below
// shows the text as written.
func flattenLogText(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)), " ")
}

// sortLogEntriesDesc orders entries newest first, as SSMS's Log File Viewer
// opens. The sort is stable, so entries sharing a second keep the order the
// log wrote them in — reversing those too would scramble a startup sequence or
// a stack dump.
func sortLogEntriesDesc(entries []*gosmo.ErrorLogEntry) []*gosmo.ErrorLogEntry {
	slices.SortStableFunc(entries, func(a, b *gosmo.ErrorLogEntry) int {
		return b.Date.Compare(a.Date)
	})
	return entries
}

// splitLogLines breaks an entry's text into the lines the log wrote. One
// xp_readerrorlog row can span several — the startup banner puts the build date
// and the OS on their own indented lines — and the details pane wraps each
// separately rather than reflowing them into one paragraph. Line breaks
// survive; indentation does not, since core.WrapText splits on strings.Fields.
func splitLogLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// selectedEntry is the entry the grid's cursor is on, nil when the grid is
// empty. Indexed against shown, which is what the grid was built from.
func (lv *LogViewer) selectedEntry() *gosmo.ErrorLogEntry {
	row := lv.grid.SelectedRow()
	if row < 0 || row >= len(lv.shown) {
		return nil
	}
	return lv.shown[row]
}

// showLogTypeMenu pops the log-family selector under its toolbar cell, reusing
// the application's context menu so the open list gets the same first-refusal
// event handling as every other overlay.
func (lv *LogViewer) showLogTypeMenu() {
	items := make([]controls.MenuItem, 0, 2)
	for _, logType := range []gosmo.ErrorLogType{gosmo.ErrorLogSQLServer, gosmo.ErrorLogAgent} {
		label := logType.String()
		if logType == lv.logType {
			label = "• " + label
		}
		items = append(items, controls.MenuItem{Label: label, Action: func() {
			if logType != lv.logType {
				// Archive numbers aren't comparable across families, so
				// switching family always lands on that family's current log.
				lv.ShowLog(logType, 0)
			}
		}})
	}
	lv.popMenu(logToolLogType, items)
}

// showLogFileMenu pops the archive selector. With no enumeration cached it
// offers the current log alone and asks for one, rather than an empty menu
// that looks like the instance has no logs.
func (lv *LogViewer) showLogFileMenu() {
	files := lv.files[lv.logType]
	if len(files) == 0 {
		lv.popMenu(logToolFile, []controls.MenuItem{
			{Label: "(log list not loaded — Refresh)", Action: lv.Refresh},
		})
		return
	}
	items := make([]controls.MenuItem, 0, len(files))
	for _, f := range files {
		num := f.Number
		label := errorLogFileLabel(f)
		if num == lv.logNum {
			label = "• " + label
		}
		items = append(items, controls.MenuItem{Label: label, Action: func() {
			lv.ShowLog(lv.logType, num)
		}})
	}
	lv.popMenu(logToolFile, items)
}

// popMenu shows items under tool i, or at the panel's top-left if that cell
// didn't fit on the row.
func (lv *LogViewer) popMenu(i int, items []controls.MenuItem) {
	r := lv.tools[i].rect
	if r.IsZero() {
		r = core.Rect{X: lv.rect.X, Y: lv.rect.Y}
	}
	lv.app.contextMenu.Show(r.X, r.Y+1, items)
}

// export writes the entries currently shown — the filtered set, not the
// whole file — to a tab-separated file.
func (lv *LogViewer) export() {
	if len(lv.shown) == 0 {
		lv.app.setStatus("Nothing to export")
		return
	}
	name := fmt.Sprintf("%s-log-%d.txt", strings.ToLower(strings.ReplaceAll(lv.logType.String(), " ", "-")), lv.logNum)
	lv.app.fileDialog.ShowSave("Export Log", name, func(path string) {
		// Rendered here on the UI goroutine, with only the write running off
		// it: applyFilter reuses shown's backing array, so a snapshot of the
		// slice would be rewritten under the goroutine by the next keystroke in
		// the filter field.
		text := lv.exportText()
		n := len(lv.shown)
		lv.app.safego("exporting a log", func() {
			// Writing on the UI goroutine freezes the app for the duration — a
			// big log to a network path is seconds, not milliseconds.
			err := fileutil.WriteAtomic(path, []byte(text), 0o644)
			lv.app.postAndWake(func() {
				if err != nil {
					lv.app.setStatus(fmt.Sprintf("Export failed: %v", err))
					return
				}
				lv.app.setStatus(fmt.Sprintf("Exported %d entries to %s", n, path))
			})
		})
		lv.app.setStatus(fmt.Sprintf("Exporting %d entries to %s...", n, path))
	})
}

// exportText renders the shown entries as the tab-separated file's contents.
func (lv *LogViewer) exportText() string {
	var b strings.Builder
	b.WriteString(strings.Join(logExportColumns, "\t") + "\n")
	for _, e := range lv.shown {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", formatSQLDate(e.Date), e.Source(), flattenLogText(e.Text))
	}
	return b.String()
}
