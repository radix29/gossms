package tui

import (
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// log_viewer_toolbar.go is the viewer's toolbar: what each cell says, when it
// is disabled and why, and the labels naming the file(s) on screen (shared with
// the grid's File column and summary line).

// Toolbar cell indexes, in buildTools' layout order. popMenu anchors a
// selector's list under its cell, and HandleKey runs the Refresh cell for F5.
const (
	logToolLogType = iota
	logToolFile
	logToolRefresh
	logToolSearch
	logToolRecycle
	logToolExport
)

// buildTools defines the toolbar: the two selectors, then the buttons, in
// logTool* order. refreshToolLabels rebuilds labels every draw.
func (lv *LogViewer) buildTools() {
	lv.tools.Cells = []controls.ToolCell{
		{Action: lv.showLogTypeMenu},
		{Action: lv.showLogFileMenu},
		{Label: "Refresh", Action: lv.Refresh},
		{Label: "Search...", Action: lv.showSearch},
		{Label: "Recycle...", Action: lv.recycleOrDelete},
		{Label: "Export...", Action: lv.export},
	}
	lv.refreshToolLabels()
}

// toolsEnabled reports whether the toolbar's actions are available *at all*:
// one answer behind drawToolbar's dimming, the click path and F5, so a dimmed
// cell can never act on a click and start a second read mid-read. One cell can
// also be withheld alone; see toolDisabled.
func (lv *LogViewer) toolsEnabled() bool { return !lv.busy }

// recycleDenied reports whether the connected login may not cycle a log, or on
// the Database Mail log, purge it (recycleRights).
//
// Recycle is the toolbar's one write, and Object Explorer's Recycle item is
// gated on the same right; gating only there left an action grey in the tree,
// live here, and failing at the server.
//
// Asked on demand rather than latched into ToolCell.Disabled as ActivityMonitor
// does: that panel rebuilds its cells every draw, while this toolbar is built
// once in NewLogViewer, where the capability probe may not have run.
func (lv *LogViewer) recycleDenied() bool {
	return !gate.Allows(lv.conn, "", lv.recycleRights()...)
}

// recycleRights are the alternatives the Recycle cell's write needs for the
// family on screen. Cycling an error log is sysadmin itself: a CONTROL SERVER
// login is refused by sp_cycle_errorlog (Msg 15247) and sp_cycle_agent_errorlog
// (Msg 14260), as is the Agent reload (gate.Sysadmin; probed on 13 and 17).
// sysmail_delete_log_sp is plain msdb permission, which db_owner in msdb has
// without any server right (W8).
func (lv *LogViewer) recycleRights() []gate.Right {
	if lv.logType == gosmo.ErrorLogDatabaseMail {
		return mailLogDeleteRights
	}
	return []gate.Right{gate.Sysadmin}
}

// mailLogDeleteRights are the alternatives that may purge the Database Mail
// log.
var mailLogDeleteRights = gate.DatabaseMailConfigRights()

// recycleOrDelete is the Recycle cell's action. The Database Mail log is a
// table with no archives to cycle into, so the cell purges old rows instead
// (deleteMailLog), the only way that log is trimmed.
func (lv *LogViewer) recycleOrDelete() {
	if lv.logType == gosmo.ErrorLogDatabaseMail {
		lv.deleteMailLog()
		return
	}
	lv.recycle()
}

// toolDisabled reports whether cell i is inert now: the whole toolbar is, or
// this cell alone is withheld.
func (lv *LogViewer) toolDisabled(i int) bool {
	return !lv.toolsEnabled() || (i == logToolRecycle && lv.recycleDenied())
}

// toolReason is what to tell the user about cell i while withheld, or "" when
// the grey speaks for itself. Same order as runTool's refusals: during a read
// every cell is grey for that reason, and naming CONTROL SERVER would name a
// right that is not why it did nothing.
func (lv *LogViewer) toolReason(i int) string {
	if !lv.toolsEnabled() {
		return ""
	}
	if i == logToolRecycle && lv.recycleDenied() {
		return gate.RequiresText(lv.recycleRights()...)
	}
	return ""
}

// showOverflowMenu pops the buttons the row was too narrow to draw under the
// "More ▾" cell, each gated as its button is.
func (lv *LogViewer) showOverflowMenu() {
	r := lv.tools.More.Rect
	if r.IsZero() {
		r = core.Rect{X: lv.rect.X, Y: lv.rect.Y}
	}
	lv.app.contextMenu.Show(r.X, r.Y+1,
		lv.tools.OverflowItems(lv.toolDisabled, lv.toolReason,
			func(i int) { lv.runTool(i) }))
}

// runTool invokes toolbar cell i's action, or says why it did not. Reports
// whether the action ran. The busy check comes first deliberately (toolReason).
func (lv *LogViewer) runTool(i int) bool {
	if !lv.toolsEnabled() {
		return false
	}
	if i == logToolRecycle && lv.recycleDenied() {
		lv.setStatus(gate.RequiresText(lv.recycleRights()...))
		return false
	}
	lv.tools.Cells[i].Action()
	return true
}

// refreshToolLabels updates the selectors' labels from the current selection,
// and the Recycle cell's from the family it acts on.
func (lv *LogViewer) refreshToolLabels() {
	lv.tools.Cells[logToolLogType].Label = "Log: " + lv.logType.String() + " ▾"
	lv.tools.Cells[logToolFile].Label = "File: " + lv.selectionLabel() + " ▾"
	lv.tools.Cells[logToolRecycle].Label = "Recycle..."
	if lv.logType == gosmo.ErrorLogDatabaseMail {
		lv.tools.Cells[logToolRecycle].Label = "Delete..."
	}
}

// fileLabel names one log file: from the cached enumeration if any, else from
// the archive number alone (the selector draws before the first enumeration).
func (lv *LogViewer) fileLabel(ref logFileRef) string {
	for _, f := range lv.files[ref.Type] {
		if f.Number == ref.Num {
			return errorLogFileLabel(f)
		}
	}
	return logFileShortLabel(ref)
}

// logFileShortLabel names a file without its date, for the grid's File column
// and status line, where errorLogFileLabel's timestamp is repeated noise.
func logFileShortLabel(ref logFileRef) string {
	if ref.Num == 0 {
		return "Current"
	}
	return fmt.Sprintf("Archive #%d", ref.Num)
}

// logFileFamilyLabel names a file with its family, for labels a mixed selection
// makes ambiguous: archive numbers aren't comparable across families, so
// "Archive #1" alone names two files.
func logFileFamilyLabel(ref logFileRef) string {
	return logFamilyShortName(ref.Type) + " " + logFileShortLabel(ref)
}

// rowFileLabel names one file as the selection needs: with the family while it
// spans both, without otherwise.
func (lv *LogViewer) rowFileLabel(ref logFileRef) string {
	if lv.multiFamily() {
		return logFileFamilyLabel(ref)
	}
	return logFileShortLabel(ref)
}

// currentFileLabel names the single file on screen, or the first of a
// selection, for callers that mean one file (the toolbar and status line use
// selectionLabel).
func (lv *LogViewer) currentFileLabel() string { return lv.fileLabel(lv.currentRef()) }

// currentRef is the first selected file, for callers that can only mean one
// (the export's suggested name). sel is never empty, but a zero-valued
// LogViewer built by a test can be.
func (lv *LogViewer) currentRef() logFileRef {
	if len(lv.sel) == 0 {
		return logFileRef{Type: lv.logType}
	}
	return lv.sel[0]
}

// multiFile reports whether more than one file is merged into the grid: what
// the File column and plural labels turn on.
func (lv *LogViewer) multiFile() bool { return len(lv.sel) > 1 }

// multiFamily reports whether the selection spans more than one log family,
// which puts the family into every file label. Nothing else turns on it.
func (lv *LogViewer) multiFamily() bool {
	for _, r := range lv.sel {
		if r.Type != lv.sel[0].Type {
			return true
		}
	}
	return false
}

// selectionFamily is the one family every ref belongs to, or fallback when the
// set is mixed or empty: how ShowLogs decides what the selectors address.
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

// scopeLabel names what the grid shows, for the status line and reading
// message: family and file for an ordinary selection, the families drawn from
// for a mixed one ("SQL Server log 3 files" would name half of it).
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

// selectionLabel names what is on screen for the file selector and status
// line: the file itself, or the count when several (four labels would overrun
// the toolbar cell).
func (lv *LogViewer) selectionLabel() string {
	if lv.multiFile() {
		return fmt.Sprintf("%d files", len(lv.sel))
	}
	return lv.currentFileLabel()
}
