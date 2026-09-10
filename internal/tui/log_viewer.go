package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
// error log file, or several of one family merged into one date-sorted grid.
// The two selectors, the filter and the grid live here; drawing is in
// log_viewer_draw.go and input in log_viewer_input.go.

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
// gosmo.Server.ReadLogFilteredContext takes. A LogViewer holds an ordered set
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

// toolsEnabled reports whether the toolbar's actions are available *at all* —
// one answer behind drawToolbar's dimming, the click path and F5, so a cell
// drawn dimmed can never still act on a click and start a second read mid-read.
// One cell can also be withheld on its own; see toolDisabled.
func (lv *LogViewer) toolsEnabled() bool { return !lv.busy }

// recycleDenied reports whether the connected login may not cycle a log.
//
// Recycle is the one write on this toolbar, and the Object Explorer's Recycle
// item is gated on the same right. Gating only there left one action answering
// two ways — grey in the tree, live here, failing at the server.
//
// Asked on demand rather than latched into toolButton.disabled the way
// ActivityMonitor does it: that panel rebuilds its cells on every draw, while
// this toolbar is built once in NewLogViewer, where the capability probe may
// not have run. A cached flag would be read from the build, and whether it had
// been refreshed by the time of a click would depend on a draw having
// happened first.
func (lv *LogViewer) recycleDenied() bool {
	return !allowsAction(lv.conn, "", rightControlServer)
}

// toolDisabled reports whether cell i is inert right now: the whole toolbar is,
// or this cell alone is withheld.
func (lv *LogViewer) toolDisabled(i int) bool {
	return !lv.toolsEnabled() || (i == logToolRecycle && lv.recycleDenied())
}

// toolReason is what to tell the user about cell i while it is withheld, or
// "" for a cell whose grey speaks for itself. Same order as runTool's refusals:
// while a read is in flight every cell is grey for that reason, and naming
// CONTROL SERVER there would name a right that is not why it did nothing.
func (lv *LogViewer) toolReason(i int) string {
	if !lv.toolsEnabled() {
		return ""
	}
	if i == logToolRecycle && lv.recycleDenied() {
		return requiresText(rightControlServer)
	}
	return ""
}

// showOverflowMenu pops the buttons the row was too narrow to draw, under the
// "More ▾" cell — each gated exactly as its button is.
func (lv *LogViewer) showOverflowMenu() {
	r := lv.more.rect
	if r.IsZero() {
		r = core.Rect{X: lv.rect.X, Y: lv.rect.Y}
	}
	lv.app.contextMenu.Show(r.X, r.Y+1,
		toolOverflowItems(lv.tools, lv.hidden, lv.toolDisabled, lv.toolReason,
			func(i int) { lv.runTool(i) }))
}

// runTool invokes toolbar cell i's action, or says why it did not. Reports
// whether the action ran.
//
// The busy check comes first deliberately: while a read is in flight every cell
// is grey for that reason, and answering a click on Recycle with "Requires
// CONTROL SERVER" would name a right that is not why it did nothing.
func (lv *LogViewer) runTool(i int) bool {
	if !lv.toolsEnabled() {
		return false
	}
	if i == logToolRecycle && lv.recycleDenied() {
		lv.setStatus(requiresText(rightControlServer))
		return false
	}
	lv.tools[i].action()
	return true
}

// refreshToolLabels updates the two selectors' labels from the current
// selection.
func (lv *LogViewer) refreshToolLabels() {
	lv.tools[logToolLogType].label = "Log: " + lv.logType.String() + " ▾"
	lv.tools[logToolFile].label = "File: " + lv.selectionLabel() + " ▾"
}

// fileLabel names one log file: from the cached enumeration when there is one,
// from the archive number alone when there isn't, since the selector is drawn
// before the first enumeration lands.
func (lv *LogViewer) fileLabel(ref logFileRef) string {
	for _, f := range lv.files[ref.Type] {
		if f.Number == ref.Num {
			return errorLogFileLabel(f)
		}
	}
	return logFileShortLabel(ref)
}

// logFileShortLabel names a file without its date — what the grid's File
// column and the status line say, where errorLogFileLabel's trailing timestamp
// is noise repeated on every row.
func logFileShortLabel(ref logFileRef) string {
	if ref.Num == 0 {
		return "Current"
	}
	return fmt.Sprintf("Archive #%d", ref.Num)
}

// logFileFamilyLabel names a file with its family, for the labels a mixed
// selection makes ambiguous: archive numbers are not comparable across
// families, so "Archive #1" alone names two different files once both are in
// the same grid.
func logFileFamilyLabel(ref logFileRef) string {
	return logFamilyShortName(ref.Type) + " " + logFileShortLabel(ref)
}

// rowFileLabel names one file the way the current selection needs it named:
// with the family while the selection spans both, without it otherwise. A
// single-family merge keeps the bare "Archive #1" it has always shown.
func (lv *LogViewer) rowFileLabel(ref logFileRef) string {
	if lv.multiFamily() {
		return logFileFamilyLabel(ref)
	}
	return logFileShortLabel(ref)
}

// currentFileLabel names the single file on screen, or the first of a
// selection. The toolbar and the status line use selectionLabel instead; this
// is for the callers that mean one specific file.
func (lv *LogViewer) currentFileLabel() string { return lv.fileLabel(lv.currentRef()) }

// currentRef is the first selected file — the one a caller that can only mean
// one file (the export's suggested name, a degenerate selection) addresses.
// sel is never empty, but a zero-valued LogViewer built by a test can be.
func (lv *LogViewer) currentRef() logFileRef {
	if len(lv.sel) == 0 {
		return logFileRef{Type: lv.logType}
	}
	return lv.sel[0]
}

// multiFile reports whether more than one file is merged into the grid — what
// the File column and the plural labels turn on. The single-file view is left
// exactly as it was.
func (lv *LogViewer) multiFile() bool { return len(lv.sel) > 1 }

// multiFamily reports whether the selection spans both log families — what
// puts the family into every file label. Nothing else turns on it: the read
// fan-out, the merge and the filter were already per-ref.
func (lv *LogViewer) multiFamily() bool {
	for _, r := range lv.sel {
		if r.Type != lv.sel[0].Type {
			return true
		}
	}
	return false
}

// selectionFamily is the one family every ref belongs to, or fallback when the
// set is mixed or empty — how ShowLogs decides what the two selectors address
// after a selection is applied.
func selectionFamily(refs []logFileRef, fallback gosmo.ErrorLogType) gosmo.ErrorLogType {
	if len(refs) == 0 {
		return fallback
	}
	for _, r := range refs {
		if r.Type != refs[0].Type {
			return fallback
		}
	}
	return refs[0].Type
}

// scopeLabel names what the grid is showing, for the status line and the
// reading message: the family and the file for an ordinary selection, and the
// families it draws from for a mixed one — "SQL Server log 3 files" would name
// only half of what is on screen.
func (lv *LogViewer) scopeLabel() string {
	if !lv.multiFamily() {
		return fmt.Sprintf("%s log %s", lv.logType, lv.selectionLabel())
	}
	names := make([]string, 0, len(logFamilies))
	for _, t := range logFamilies {
		if slices.ContainsFunc(lv.sel, func(r logFileRef) bool { return r.Type == t }) {
			names = append(names, logFamilyShortName(t))
		}
	}
	return strings.Join(names, " + ") + " logs " + lv.selectionLabel()
}

// selectionLabel names what is on screen for the file selector and the status
// line: the file itself when there is one, the count when there are several.
// Listing four archives' labels would overrun the toolbar cell and every
// status line that names the selection.
func (lv *LogViewer) selectionLabel() string {
	if lv.multiFile() {
		return fmt.Sprintf("%d files", len(lv.sel))
	}
	return lv.currentFileLabel()
}

// ShowLog points the panel at a single log file and reads it. Reopening from
// another tree node comes through here, so an already-open viewer switches
// files instead of a second one being created — and lands on the one-file view
// however many files were merged before.
func (lv *LogViewer) ShowLog(logType gosmo.ErrorLogType, logNum int) {
	lv.ShowLogs(logType, []logFileRef{{Type: logType, Num: logNum}})
}

// ShowLogs points the panel at a set of files and reads them. The set may span
// both families; logType is what the selectors fall back to when it does.
// An empty set is the current log of logType: a selection the user emptied
// would otherwise leave the grid with nothing to describe and no way back.
func (lv *LogViewer) ShowLogs(logType gosmo.ErrorLogType, refs []logFileRef) {
	if len(refs) == 0 {
		refs = []logFileRef{{Type: logType, Num: 0}}
	}
	lv.sel = slices.Clone(refs)
	// By family first, then by archive number. The merge order is the
	// selection's order (see sortLogRowsDesc), so a set the checklist built by
	// ticking rows in whatever order the user reached them has to be brought
	// back to one canonical order — otherwise the same two files merge
	// differently depending on which was ticked first.
	slices.SortFunc(lv.sel, func(a, b logFileRef) int {
		if a.Type != b.Type {
			return int(a.Type) - int(b.Type)
		}
		return a.Num - b.Num
	})
	// The selectors still address exactly one family, since Recycle and the
	// single-file picker can only mean one: the selection's own family when it
	// has one, and whatever was on screen before when it is mixed.
	lv.logType = selectionFamily(lv.sel, logType)
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

// Load reads the selected files in the background and applies the merged
// result on the UI goroutine. The family's enumeration rides along, so the file
// selector has its list without a second round trip.
func (lv *LogViewer) Load() {
	if !lv.app.isConnected(lv.conn) {
		lv.entries, lv.shown, lv.readErrs = nil, nil, nil
		lv.grid.SetData(lv.gridColumns(), nil)
		lv.setStatus("Not connected")
		return
	}
	lv.cancelRead()
	lv.seq++
	seq := lv.seq
	lv.busy = true
	lv.setStatus(fmt.Sprintf("Reading %s%s...", lv.scopeLabel(), lv.searchSuffix()))
	lv.refreshToolLabels()

	search := lv.search
	// Snapshotted, not read from lv on the goroutine: the selection can be
	// changed again while this read is out, and the result has to describe the
	// files it actually asked for.
	refs := slices.Clone(lv.sel)
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
		// Both families, not only the one on screen: the file checklist offers
		// a cross-family selection, so it needs the other family's archive
		// numbering before the user opens it — and fetching that lazily would
		// put a round trip behind a menu keypress. A family that cannot be
		// enumerated (an instance with no Agent) is simply left out of the
		// checklist, exactly as it is today.
		//
		// The enumeration runs alongside the reads rather than ahead of them:
		// nothing in the read depends on it, and the two families cost ~50 ms
		// that came straight off a ~150 ms load on 2016 and 2017. (2025 shows
		// no gain — it appears to serialise the two server-side — and no loss.)
		enums := make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile, len(logFamilies))
		var enumerated sync.WaitGroup
		enumerated.Add(1)
		lv.app.safego("enumerating error logs", func() {
			defer enumerated.Done()
			for _, t := range logFamilies {
				enumCtx, enumCancel := context.WithTimeout(ctx, logReadTimeout)
				files, err := sc.Server.EnumErrorLogsContext(enumCtx, t)
				enumCancel()
				if err == nil {
					enums[t] = files
				}
			}
		})
		rows, readErrs := readLogFiles(lv.app, ctx, sc, refs, search)
		enumerated.Wait()
		lv.app.postAndWake(func() {
			if seq != lv.seq {
				return
			}
			lv.busy = false
			lv.cancel = nil
			for t, files := range enums {
				lv.files[t] = files
			}
			lv.refreshToolLabels()
			// Only a selection where *nothing* could be read is an error: with
			// one archive unreadable out of four, the grid holds the other
			// three and summary says how many landed.
			if len(readErrs) == len(refs) && len(readErrs) > 0 {
				lv.entries, lv.shown, lv.readErrs = nil, nil, nil
				lv.grid.SetError(displayError(readErrs[0].err))
				return
			}
			lv.readErrs = readErrs
			lv.entries = sortLogRowsDesc(rows)
			lv.applyFilter() // resets detailScroll itself
		})
	})
}

// readLogFiles reads every ref and returns the rows in ref order together with
// whichever files failed. It runs on the read goroutine, off the UI one.
//
// Each read gets its own logReadTimeout deadline under the panel's one
// cancellable ctx: sharing a single deadline across N files would let the first
// slow archive eat the budget of the ones behind it. The pool is bounded for
// the reason the Databases folder's is — an instance can be configured to keep
// 99 archives, and xp_readerrorlog parses the file server-side.
//
// Results are collected into a slice indexed by ref rather than appended as
// they finish, so the merge order is the selection's order however the reads
// interleave — which is what makes a timestamp tie break the same way twice.
func readLogFiles(app *App, ctx context.Context, sc *db.ServerConn, refs []logFileRef, search gosmo.LogSearch) ([]logRow, []logFileError) {
	per := make([][]logRow, len(refs))
	errs := make([]error, len(refs))
	// Seeded failed and cleared on success, not the other way round: a worker
	// whose read panicked never reaches either assignment, and a file that was
	// never read must be reported as unread rather than passing for an empty
	// one — an empty archive and an archive nobody read look identical here.
	for i := range errs {
		errs[i] = errLogFileNotRead
	}

	// No onPanic: the errLogFileNotRead seed already reports a panicked read.
	app.fanOut(len(refs), "reading an error log", func(i int) {
		readCtx, cancel := context.WithTimeout(ctx, logReadTimeout)
		defer cancel()
		entries, err := sc.Server.ReadLogFilteredContext(readCtx, refs[i].Type, refs[i].Num, search)
		if err != nil {
			errs[i] = err
			return
		}
		rows := make([]logRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, logRow{entry: e, ref: refs[i]})
		}
		per[i], errs[i] = rows, nil
	}, nil)

	total := 0
	for _, rows := range per {
		total += len(rows)
	}
	out := make([]logRow, 0, total)
	var failed []logFileError
	for i := range refs {
		if errs[i] != nil {
			failed = append(failed, logFileError{ref: refs[i], err: errs[i]})
			continue
		}
		out = append(out, per[i]...)
	}
	return out, failed
}

// errLogFileNotRead is readLogFiles' seed for a file whose read never
// finished — it panicked, and fanOut recovered it and moved on, leaving the
// seed. It is never the reason a read *failed*, only the reason one is missing.
var errLogFileNotRead = errors.New("the read did not finish")

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
				lv.reanchorAfterCycle(logType)
				lv.Refresh()
			})
		})
	})
}

// reanchorAfterCycle drops a multi-file selection back to the current log when
// a family it draws from has just been cycled.
//
// A cycle renumbers every archive one higher and deletes the oldest, so the
// numbers a set was chosen by no longer name the files it was chosen from —
// re-reading them would silently hand back a different set, one file of which
// may not exist any more. A single-file view keeps its number, which is the
// behaviour it has always had: the user asked for "Archive #1" and gets
// whatever is now Archive #1.
//
// A mixed selection is re-anchored by *any* family in it being cycled, not
// only the one the selectors address: half a merged set going stale is the
// same silent lie as all of it, and the cycled family is the one the user was
// just looking at.
func (lv *LogViewer) reanchorAfterCycle(logType gosmo.ErrorLogType) {
	if !lv.multiFile() || !slices.ContainsFunc(lv.sel, func(r logFileRef) bool { return r.Type == logType }) {
		return
	}
	lv.logType = logType
	lv.sel = []logFileRef{{Type: logType, Num: 0}}
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

// logGridColumnsMulti is logGridColumns with the File column a merged view
// needs. Source is the log's own ProcessInfo/severity and says nothing about
// which *file* a row came from, so a merged grid without this column cannot be
// read at all. It appears only while more than one file is selected — the
// single-file view is untouched.
var logGridColumnsMulti = []string{"Date ▼", "File", "Source", "Message"}

// logExportColumns are the same columns without the sort marker: an exported
// header row names the column rather than describing the grid.
var logExportColumns = []string{"Date", "Source", "Message"}

// logExportColumnsMulti is logExportColumns with the File column, on the same
// condition the grid's is.
var logExportColumnsMulti = []string{"Date", "File", "Source", "Message"}

// gridColumns and exportColumns are the headers for the current selection.
func (lv *LogViewer) gridColumns() []string {
	if lv.multiFile() {
		return logGridColumnsMulti
	}
	return logGridColumns
}

func (lv *LogViewer) exportColumns() []string {
	if lv.multiFile() {
		return logExportColumnsMulti
	}
	return logExportColumns
}

// cells renders one row for the grid and the export, which share a shape.
func (lv *LogViewer) cells(r logRow) []string {
	if lv.multiFile() {
		return []string{formatSQLDate(r.entry.Date), lv.rowFileLabel(r.ref), r.entry.Source(), flattenLogText(r.entry.Text)}
	}
	return []string{formatSQLDate(r.entry.Date), r.entry.Source(), flattenLogText(r.entry.Text)}
}

// applyFilter rebuilds shown from entries and hands it to the grid, matching a
// case-insensitive substring over the source and message. An empty filter
// shows everything.
func (lv *LogViewer) applyFilter() {
	lv.invalidateDetailCache()
	needle := strings.ToLower(strings.TrimSpace(lv.filter.Value()))
	lv.shown = lv.shown[:0]
	for _, r := range lv.entries {
		if needle == "" || logEntryMatches(r.entry, needle) {
			lv.shown = append(lv.shown, r)
		}
	}
	rows := make([][]string, 0, len(lv.shown))
	for _, r := range lv.shown {
		rows = append(rows, lv.cells(r))
	}
	lv.grid.SetData(lv.gridColumns(), rows)
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
	switch {
	case len(lv.entries) == 0:
		return fmt.Sprintf("%s%s — no entries%s", lv.scopeLabel(), lv.searchSuffix(), lv.readErrSuffix())
	case len(lv.shown) == len(lv.entries):
		return fmt.Sprintf("%s%s — %d entries%s", lv.scopeLabel(), lv.searchSuffix(), len(lv.entries), lv.readErrSuffix())
	default:
		return fmt.Sprintf("%s%s — %d of %d entries match the filter%s",
			lv.scopeLabel(), lv.searchSuffix(), len(lv.shown), len(lv.entries), lv.readErrSuffix())
	}
}

// readErrSuffix says how much of the selection the grid is actually showing,
// or "" when every file was read. A merged read that dropped one archive shows
// the rest, so without this the panel would silently be short a file — and the
// entry count alone cannot say so.
func (lv *LogViewer) readErrSuffix() string {
	if len(lv.readErrs) == 0 {
		return ""
	}
	first := lv.readErrs[0]
	return fmt.Sprintf(" — %d of %d files read (%s: %v)",
		len(lv.sel)-len(lv.readErrs), len(lv.sel), lv.rowFileLabel(first.ref), displayError(first.err))
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

// sortLogRowsDesc is sortLogEntriesDesc over merged rows. The sort is stable
// and the input is in selection order, file by file, so a timestamp shared
// across two files breaks by file and then by position within the file — the
// same order every time. An unstable sort, or a merge in completion order,
// would reorder same-second rows under the cursor on every refresh.
func sortLogRowsDesc(rows []logRow) []logRow {
	slices.SortStableFunc(rows, func(a, b logRow) int {
		return b.entry.Date.Compare(a.entry.Date)
	})
	return rows
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

// selectedLogRow is the row the grid's cursor is on, and whether there is one.
// Indexed against shown, which is what the grid was built from.
func (lv *LogViewer) selectedLogRow() (logRow, bool) {
	row := lv.grid.SelectedRow()
	if row < 0 || row >= len(lv.shown) {
		return logRow{}, false
	}
	return lv.shown[row], true
}

// showLogTypeMenu pops the log-family selector under its toolbar cell, reusing
// the application's context menu so the open list gets the same first-refusal
// event handling as every other overlay.
func (lv *LogViewer) showLogTypeMenu() {
	items := make([]controls.MenuItem, 0, len(logFamilies))
	for _, logType := range logFamilies {
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

// showLogFileMenu pops the archive selector for the family the selectors
// address. With nothing enumerated at all it offers Refresh instead, rather
// than an empty menu that looks like the instance has no logs.
//
// Picking a file here always narrows to that one file, however many were
// merged before — the one-click case stays what it was. Merging, including
// across families, is the checklist below, reached from the last entry.
func (lv *LogViewer) showLogFileMenu() {
	files := lv.files[lv.logType]
	// The other family's list is enough to open the checklist on: an instance
	// whose Agent log cannot be enumerated must not lock the user out of
	// merging the SQL Server files, and the reverse.
	if len(files) == 0 && len(lv.enumeratedFamilies()) == 0 {
		lv.popMenu(logToolFile, []controls.MenuItem{
			{Label: "(log list not loaded — Refresh)", Action: lv.Refresh},
		})
		return
	}
	items := make([]controls.MenuItem, 0, len(files)+2)
	for _, f := range files {
		num := f.Number
		label := errorLogFileLabel(f)
		// Every selected file is bulleted, not just the first: with three
		// merged, one bullet would name the grid's contents wrongly.
		if lv.isSelected(logFileRef{Type: lv.logType, Num: num}) {
			label = "• " + label
		}
		items = append(items, controls.MenuItem{Label: label, Action: func() {
			lv.ShowLog(lv.logType, num)
		}})
	}
	items = append(items,
		controls.MenuItem{Divider: true},
		controls.MenuItem{Label: "Select Files...", Action: func() {
			// Seeded here rather than in showLogFileChecklist, which every
			// toggle re-enters: seeding there would undo the tick that called
			// it.
			lv.pending = slices.Clone(lv.sel)
			lv.showLogFileChecklist()
		}})
	lv.popMenu(logToolFile, items)
}

// isSelected reports whether ref is one of the files on screen.
func (lv *LogViewer) isSelected(ref logFileRef) bool { return slices.Contains(lv.sel, ref) }

// enumeratedFamilies are the families whose file lists are cached, in
// logFamilies order — what the checklist can offer. A family the instance does
// not have (no Agent) never enumerates and simply does not appear.
func (lv *LogViewer) enumeratedFamilies() []gosmo.ErrorLogType {
	out := make([]gosmo.ErrorLogType, 0, len(logFamilies))
	for _, t := range logFamilies {
		if len(lv.files[t]) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// showLogFileChecklist pops the multi-file picker: one tickable row per file,
// every enumerated family in turn, then Select All / Clear, then the entry that
// reads the ticked set. Nothing is read until that last entry is chosen — the
// toggles edit pending, so dismissing the menu leaves the grid describing the
// files it actually holds.
//
// The set may span both families. Archive numbers are not comparable across
// them, which is why the family selector, the file list and Recycle each still
// mean exactly one family — but a merged read is per-ref and a row carries its
// own logFileRef, so a mixed set costs only the family in the labels. Reading
// the SQL Server and Agent logs of the same minute side by side is the whole
// reason to look at the Agent log at all.
func (lv *LogViewer) showLogFileChecklist() {
	families := lv.enumeratedFamilies()
	if len(families) == 0 {
		lv.popMenu(logToolFile, []controls.MenuItem{
			{Label: "(log list not loaded — Refresh)", Action: lv.Refresh},
		})
		return
	}
	// Named only when there is something to tell apart: with one family
	// enumerated the rows are what they have always been.
	named := len(families) > 1
	items := make([]controls.MenuItem, 0, 8)
	var all []logFileRef
	for i, t := range families {
		if i > 0 {
			items = append(items, controls.MenuItem{Divider: true})
		}
		for _, f := range lv.files[t] {
			ref := logFileRef{Type: t, Num: f.Number}
			all = append(all, ref)
			mark := "☐ "
			if slices.Contains(lv.pending, ref) {
				mark = "☑ "
			}
			label := errorLogFileLabel(f)
			if named {
				label = logFamilyShortName(t) + " — " + label
			}
			// The item's own index, taken as it is appended: the dividers
			// between families make it no longer the file's position in the
			// list, and SetHover below addresses menu rows.
			row := len(items)
			items = append(items, controls.MenuItem{Label: mark + label, Action: func() {
				if j := slices.Index(lv.pending, ref); j >= 0 {
					lv.pending = slices.Delete(lv.pending, j, j+1)
				} else {
					lv.pending = append(lv.pending, ref)
				}
				lv.showLogFileChecklist()
				// Re-showing resets the hover to nothing, which for a keyboard
				// user means every tick sends the cursor back to the top of the
				// list. Put it back on the row they just ticked.
				lv.app.contextMenu.SetHover(row)
			}})
		}
	}
	items = append(items,
		controls.MenuItem{Divider: true},
		controls.MenuItem{Label: "Select All", Action: func() {
			lv.pending = all
			lv.showLogFileChecklist()
		}},
		controls.MenuItem{Label: "Clear", Action: func() {
			lv.pending = nil
			lv.showLogFileChecklist()
		}},
		controls.MenuItem{Divider: true},
		controls.MenuItem{
			Label:   lv.checklistApplyLabel(named),
			Enabled: func() bool { return len(lv.pending) > 0 },
			Note:    "tick at least one file",
			Action:  func() { lv.ShowLogs(lv.logType, lv.pending) },
		})
	lv.popMenu(logToolFile, items)
}

// checklistApplyLabel names what the checklist's last entry will read, so the
// count is visible before the menu closes. named carries the checklist's own
// rule for a single file: the family is part of the name only while more than
// one family is on offer to tell apart.
func (lv *LogViewer) checklistApplyLabel(named bool) string {
	if len(lv.pending) == 1 {
		if named {
			return "Read " + logFileFamilyLabel(lv.pending[0])
		}
		return "Read " + logFileShortLabel(lv.pending[0])
	}
	return fmt.Sprintf("Read %d files", len(lv.pending))
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
	family := strings.ToLower(strings.ReplaceAll(lv.logType.String(), " ", "-"))
	name := fmt.Sprintf("%s-log-%d.txt", family, lv.currentRef().Num)
	switch {
	case lv.multiFamily():
		// The family in the name would be the selectors' one, which is not
		// what the file holds.
		name = fmt.Sprintf("error-log-%dfiles.txt", len(lv.sel))
	case lv.multiFile():
		name = fmt.Sprintf("%s-log-%dfiles.txt", family, len(lv.sel))
	}
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
	b.WriteString(strings.Join(lv.exportColumns(), "\t") + "\n")
	for _, r := range lv.shown {
		b.WriteString(strings.Join(lv.cells(r), "\t") + "\n")
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
				a.refreshOpenLogViewer(sc, logType)
			})
		})
	})
}

// refreshOpenLogViewer re-reads the LogViewer open on sc, if there is one,
// after logType has been cycled. There is at most one per connection — see
// showLogViewerFor.
func (a *App) refreshOpenLogViewer(sc *db.ServerConn, logType gosmo.ErrorLogType) {
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		lv, ok := p.(*LogViewer)
		return ok && lv.conn == sc
	})
	if idx >= 0 {
		lv := a.panels.PanelAt(idx).(*LogViewer)
		lv.reanchorAfterCycle(logType)
		lv.Refresh()
	}
}
