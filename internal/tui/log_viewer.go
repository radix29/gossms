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

// LogViewer is the Log File Viewer panel: a grid of one log file's entries over
// a details pane showing the selected entry in full, with the log family and
// archive number chosen from the toolbar.
//
// Reads run on the panel's host connection rather than one of its own — each is
// a one-shot query bounded by logReadTimeout with nothing on a timer, so no
// background traffic queues behind the shared connection.
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

	// search is the server-side narrowing in force — xp_readerrorlog's own
	// search strings and date range, edited by the Search dialog; zero means the
	// whole file. Distinct from the filter field below, which narrows what was
	// already read. See log_search_dialog.go.
	search gosmo.LogSearch

	// entries is everything the last read returned, shown the filtered subset
	// the grid is built from. entries is kept whole so clearing the filter needs
	// no second read.
	entries []*gosmo.ErrorLogEntry
	shown   []*gosmo.ErrorLogEntry

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
	// toolsEnd is the column just past the last laid-out button, where the
	// filter field starts — mirrors ActivityMonitor.toolsEnd.
	toolsEnd int

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

	// seq guards against a superseded read landing after a newer one: every load
	// increments it, and an async result applies only if still the most recent.
	// Same role as DetailBrowser.seq.
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

// cancelRead aborts the in-flight read, on close and when a new read supersedes
// one. seq already discards a superseded result, but without cancelling the
// query runs on the shared host connection until logReadTimeout.
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

// layoutChildren gives the grid and the details pane their halves of the area
// below the toolbar, on every resize and after every splitter drag.
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

// Toolbar cell indexes, in buildTools' layout order. Cells are addressed by
// index: popMenu anchors a selector's list under its own cell, and HandleKey
// runs the Refresh cell's action for F5.
const (
	logToolLogType = iota
	logToolFile
	logToolRefresh
	logToolSearch
	logToolRecycle
	logToolExport
)

// buildTools defines the toolbar: the two selectors, then the buttons, in the
// order the logTool* constants name. refreshToolLabels rebuilds the labels on
// every draw, since both selectors show what they point at.
func (lv *LogViewer) buildTools() {
	lv.tools = []toolButton{
		{action: lv.showLogTypeMenu},
		{action: lv.showLogFileMenu},
		{label: "Refresh", action: lv.Refresh},
		{label: "Search...", action: lv.showSearch},
		{label: "Recycle...", action: lv.recycle},
		{label: "Export...", action: lv.export},
	}
	lv.refreshToolLabels()
}

// toolsEnabled reports whether the toolbar's actions are available — one answer
// behind drawToolbar's dimming, the click path and F5, so a cell drawn dimmed
// can never still act on a click and start a second read mid-read.
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

// currentFileLabel names the file on screen: from the cached enumeration when
// there is one, from the archive number alone when there isn't, since the
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

// ShowLog points the panel at a log file and reads it. Reopening from another
// tree node comes through here, so an already-open viewer switches files
// instead of a second one being created.
func (lv *LogViewer) ShowLog(logType gosmo.ErrorLogType, logNum int) {
	lv.logType, lv.logNum = logType, logNum
	lv.detailScroll = 0
	lv.Load()
}

// Refresh re-reads the current file and re-enumerates both families (F5 or the
// toolbar). The enumeration is dropped rather than refreshed: a cycled log
// renumbers every archive, so only a fresh read corrects the cached list.
func (lv *LogViewer) Refresh() {
	lv.files = make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile)
	lv.Load()
}

// Load reads the current file in the background and applies the result on the
// UI goroutine. The same family's enumeration rides along, so the file selector
// has its list without a second round trip.
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
	lv.setStatus(fmt.Sprintf("Reading %s log %s%s...", lv.logType, lv.currentFileLabel(), lv.searchSuffix()))
	lv.refreshToolLabels()

	logType, logNum, search := lv.logType, lv.logNum, lv.search
	sc := lv.conn
	// One cancel for the panel to pull, but a fresh deadline per call: sharing
	// one logReadTimeout lets a slow sp_enumerrorlogs eat the read's half of it,
	// timing out the file the user asked for because the *list* was slow.
	ctx, cancel := context.WithCancel(sc.Context())
	lv.cancel = cancel
	// safegoRepair, not safego: busy is cleared in the callback below, which a
	// panic on the read goroutine never reaches, and toolsEnabled gates the
	// whole toolbar on it — Refresh, Export and both selectors would sit inert
	// until the panel was closed.
	lv.app.safegoRepair("reading an error log", func() { lv.readPanicked(seq) }, func() {
		defer cancel()
		enumCtx, enumCancel := context.WithTimeout(ctx, logReadTimeout)
		files, filesErr := sc.Server.EnumErrorLogsContext(enumCtx, logType)
		enumCancel()
		readCtx, readCancel := context.WithTimeout(ctx, logReadTimeout)
		defer readCancel()
		entries, err := sc.Server.ReadLogFilteredContext(readCtx, logType, logNum, search)
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
				lv.grid.SetError(displayError(err))
				return
			}
			lv.entries = sortLogEntriesDesc(entries)
			lv.applyFilter() // resets detailScroll itself
		})
	})
}

// readPanicked releases the busy latch after a panic on the read goroutine —
// Load's safegoRepair step. Guarded by seq like the normal completion path: a
// newer Load set busy for itself, and clearing it here would re-enable a
// toolbar whose read is still out.
func (lv *LogViewer) readPanicked(seq int) {
	if seq != lv.seq {
		return
	}
	lv.busy = false
	lv.cancel = nil
	lv.refreshToolLabels()
	lv.setStatus("Read stopped unexpectedly — see the log for details")
}

// recycle closes the current log of the family on screen and starts a new one,
// after confirming. On success it reloads, replacing the archive numbering the
// file selector draws from — the cycle renumbered all of it.
func (lv *LogViewer) recycle() {
	if !lv.app.requireConn(lv.conn) {
		return
	}
	sc, logType := lv.conn, lv.logType
	// Latched before the question, not in the answer: busy is what stops a read
	// starting underneath the cycle, and the confirm dialog doesn't stop F5
	// reaching the panel — a Load begun while the question was up would clear
	// busy from under the cycle it knows nothing about.
	lv.busy = true
	lv.app.confirmDialog.ShowConfirm("Recycle Log", cycleLogMessage(logType, sc.Opts.Server), func(confirmed bool) {
		if !confirmed {
			lv.busy = false
			return
		}
		lv.setStatus(fmt.Sprintf("Recycling the %s error log...", logType))
		// safegoRepair for the same reason Load uses it: busy is cleared in the
		// posted callback, which a panic never reaches, and toolsEnabled gates
		// the whole toolbar on it.
		lv.app.safegoRepair("cycling an error log", lv.recyclePanicked, func() {
			ctx, cancel := context.WithTimeout(sc.Context(), logReadTimeout)
			defer cancel()
			err := sc.Server.CycleLogContext(ctx, logType)
			lv.app.postAndWake(func() {
				lv.busy = false
				if err != nil {
					lv.setStatus(fmt.Sprintf("Recycle failed: %v", withPermissionAdvice(err)))
					return
				}
				lv.Refresh()
			})
		})
	})
}

// recyclePanicked releases the busy latch after a panic on the cycle goroutine
// — recycle's safegoRepair step. No seq guard, unlike readPanicked: busy was
// held across the whole cycle, so nothing else can have started.
func (lv *LogViewer) recyclePanicked() {
	lv.busy = false
	lv.setStatus("Recycle stopped unexpectedly — see the log for details")
}

// cycleLogMessage is the confirmation question for recycling a log, shared by
// the toolbar and the Object Explorer folder's menu. It names what is lost: the
// archives are renumbered, and the instance drops the oldest once holding as
// many as it is configured to keep.
func cycleLogMessage(logType gosmo.ErrorLogType, server string) string {
	return fmt.Sprintf(
		"Close the current %s error log on %s and start a new one?\n\n"+
			"Each archive is renumbered one higher, and the oldest is deleted once "+
			"the instance holds as many archives as it is configured to keep.",
		logType, server)
}

// logGridColumns are the entry grid's columns. The marker on Date says which
// way rows are ordered; Source is whichever of ProcessInfo and ErrorLevel the
// log family populates.
var logGridColumns = []string{"Date ▼", "Source", "Message"}

// logExportColumns are the same columns without the sort marker: an exported
// header row names the column rather than describing the grid.
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
// name the log file — a fresh enumeration can rename "Archive #3" without the
// selected entry changing.
func (lv *LogViewer) invalidateDetailCache() {
	lv.detailCacheEntry, lv.detailCache = nil, nil
}

// summary is the status line under the grid: how much of the file is shown,
// which file it is, and what the server was asked for when a search is in
// force. Naming the search matters — "no entries" on a searched read means the
// search found nothing, not that the log is empty.
func (lv *LogViewer) summary() string {
	if len(lv.entries) == 0 {
		return fmt.Sprintf("%s log %s%s — no entries", lv.logType, lv.currentFileLabel(), lv.searchSuffix())
	}
	if len(lv.shown) == len(lv.entries) {
		return fmt.Sprintf("%s log %s%s — %d entries", lv.logType, lv.currentFileLabel(), lv.searchSuffix(), len(lv.entries))
	}
	return fmt.Sprintf("%s log %s%s — %d of %d entries match the filter",
		lv.logType, lv.currentFileLabel(), lv.searchSuffix(), len(lv.shown), len(lv.entries))
}

// searchSuffix describes the server-side search for the status line, or "" if
// there is none.
func (lv *LogViewer) searchSuffix() string {
	parts := make([]string, 0, 3)
	if lv.search.Text1 != "" {
		parts = append(parts, fmt.Sprintf("%q", lv.search.Text1))
	}
	if lv.search.Text2 != "" {
		parts = append(parts, fmt.Sprintf("%q", lv.search.Text2))
	}
	if !lv.search.From.IsZero() || !lv.search.To.IsZero() {
		parts = append(parts, fmt.Sprintf("%s..%s",
			orDefault(formatLogSearchTime(lv.search.From), "…"),
			orDefault(formatLogSearchTime(lv.search.To), "…")))
	}
	if len(parts) == 0 {
		return ""
	}
	return " searching " + strings.Join(parts, " + ")
}

// showSearch opens the Search dialog and re-reads with whatever it returns,
// unconditionally, including for an unchanged search: a press that appeared to
// do nothing would read as the dialog having failed.
func (lv *LogViewer) showSearch() {
	if !lv.app.requireConn(lv.conn) {
		return
	}
	lv.app.logSearchDialog.ShowLogSearch(lv.search, func(search gosmo.LogSearch) {
		lv.search = search
		lv.detailScroll = 0
		lv.Load()
	})
}

// setStatus writes the panel's one-line state into the grid's own status bar,
// so it sits with the rows it describes.
func (lv *LogViewer) setStatus(s string) { lv.grid.SetStatus(s) }

// logEntryMatches reports whether needle (already lowercased) appears in the
// entry's source or message.
func logEntryMatches(e *gosmo.ErrorLogEntry, needle string) bool {
	return strings.Contains(strings.ToLower(e.Text), needle) ||
		strings.Contains(strings.ToLower(e.Source()), needle)
}

// flattenLogText makes one grid line out of a log entry's text. An entry can
// carry embedded newlines and tabs — the startup banner spans four lines — and
// a grid cell is one row tall, so they become spaces. The details pane shows
// the text as written.
func flattenLogText(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)), " ")
}

// sortLogEntriesDesc orders entries newest first, as SSMS's Log File Viewer
// opens. The sort is stable, so entries sharing a second keep the order the log
// wrote them in — reversing those would scramble a startup sequence or a stack
// dump.
func sortLogEntriesDesc(entries []*gosmo.ErrorLogEntry) []*gosmo.ErrorLogEntry {
	slices.SortStableFunc(entries, func(a, b *gosmo.ErrorLogEntry) int {
		return b.Date.Compare(a.Date)
	})
	return entries
}

// splitLogLines breaks an entry's text into the lines the log wrote. One
// xp_readerrorlog row can span several — the startup banner puts the build date
// and the OS on their own indented lines — and the details pane wraps each
// separately rather than reflowing them into a paragraph. Line breaks survive;
// indentation does not, since core.WrapText splits on strings.Fields.
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
// offers the current log alone and asks for one, rather than an empty menu that
// looks like the instance has no logs.
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

// export writes the entries currently shown — the filtered set, not the whole
// file — to a tab-separated file.
func (lv *LogViewer) export() {
	if len(lv.shown) == 0 {
		lv.app.setStatus("Nothing to export")
		return
	}
	name := fmt.Sprintf("%s-log-%d.txt", strings.ToLower(strings.ReplaceAll(lv.logType.String(), " ", "-")), lv.logNum)
	lv.app.fileDialog.ShowSave("Export Log", name, func(path string) {
		// Rendered here on the UI goroutine, with only the write running off it:
		// applyFilter reuses shown's backing array, so a snapshot of the slice
		// would be rewritten under the goroutine by the next keystroke in the
		// filter field.
		text := lv.exportText()
		n := len(lv.shown)
		lv.app.safego("exporting a log", func() {
			// Writing on the UI goroutine would freeze the app: a big log to a
			// network path takes seconds.
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

// recycleLogFrom cycles a log family from its Object Explorer folder, then
// refreshes the folder so the renumbered archives appear under it.
//
// Any LogViewer open on the same connection is refreshed too, through Refresh
// rather than Load, because the viewer may be sitting on the *other* family:
// Load re-enumerates only the family on screen, so the cycled one's cached
// numbering would survive until the user flipped the selector to it and opened
// an archive its label no longer named.
func (a *App) recycleLogFrom(sc *db.ServerConn, logType gosmo.ErrorLogType, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.confirmDialog.ShowConfirm("Recycle Log", cycleLogMessage(logType, sc.Opts.Server), func(confirmed bool) {
		if !confirmed {
			return
		}
		a.setStatus(fmt.Sprintf("Recycling the %s error log...", logType))
		a.safego("cycling an error log", func() {
			ctx, cancel := context.WithTimeout(sc.Context(), logReadTimeout)
			defer cancel()
			err := sc.Server.CycleLogContext(ctx, logType)
			a.postAndWake(func() {
				if err != nil {
					a.setStatus(fmt.Sprintf("Recycle failed: %v", withPermissionAdvice(err)))
					return
				}
				a.setStatus(fmt.Sprintf("%s error log recycled", logType))
				refreshExplorerNode(a, node)
				a.refreshOpenLogViewer(sc)
			})
		})
	})
}

// refreshOpenLogViewer re-reads the LogViewer open on sc, if there is one.
// There is at most one per connection — see showLogViewerFor.
func (a *App) refreshOpenLogViewer(sc *db.ServerConn) {
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		lv, ok := p.(*LogViewer)
		return ok && lv.conn == sc
	})
	if idx >= 0 {
		a.panels.PanelAt(idx).(*LogViewer).Refresh()
	}
}
