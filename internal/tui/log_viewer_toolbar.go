package tui

import (
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// log_viewer_toolbar.go is the viewer's toolbar: what each cell says, when it
// is disabled and why, and the labels that name the file or files on screen —
// the same wording the grid's File column and the summary line use. The panel
// itself is in log_viewer.go.

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
	return !gate.Allows(lv.conn, "", gate.ControlServer)
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
		return gate.RequiresText(gate.ControlServer)
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
		lv.setStatus(gate.RequiresText(gate.ControlServer))
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
