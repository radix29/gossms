package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// log_viewer_files.go is how a file is chosen (family menu, file menu, the
// multi-file checklist), plus Export and the App-level entry points that
// recycle a log from the tree and refresh an open viewer.

// showLogTypeMenu pops the log-family selector under its toolbar cell, reusing
// the application's context menu for the same first-refusal event handling as
// every other overlay.
func (lv *LogViewer) showLogTypeMenu() {
	items := make([]controls.MenuItem, 0, len(logFamilies))
	for _, logType := range logFamilies {
		label := logType.String()
		if logType == lv.logType {
			label = "• " + label
		}
		items = append(items, controls.MenuItem{Label: label, Action: func() {
			if logType != lv.logType {
				// Archive numbers aren't comparable across families, so switching family
				// lands on that family's current log.
				lv.ShowLog(logType, 0)
			}
		}})
	}
	lv.popMenu(logToolLogType, items)
}

// showLogFileMenu pops the archive selector for the family the selectors
// address. With nothing enumerated it offers Refresh rather than an empty menu
// that looks like an instance with no logs.
//
// Picking a file here always narrows to that one file, however many were
// merged before. Merging, including across families, is the checklist reached
// from the last entry.
func (lv *LogViewer) showLogFileMenu() {
	files := lv.files[lv.logType]
	// The other family's list suffices to open the checklist: an instance whose
	// Agent log cannot be enumerated must not lock the user out of merging the SQL
	// Server files, and the reverse.
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
		// Every selected file is bulleted: with three merged, one bullet would
		// misdescribe the grid.
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
			// Seeded here, not in showLogFileChecklist, which every toggle re-enters:
			// seeding there would undo the tick that called it.
			lv.pending = slices.Clone(lv.sel)
			lv.showLogFileChecklist()
		}})
	lv.popMenu(logToolFile, items)
}

// isSelected reports whether ref is one of the files on screen.
func (lv *LogViewer) isSelected(ref logFileRef) bool { return slices.Contains(lv.sel, ref) }

// enumeratedFamilies are the families whose file lists are cached, in
// logFamilies order: what the checklist can offer. A family the instance lacks
// (no Agent) never enumerates and does not appear.
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
// reads the ticked set. Toggles edit pending, so dismissing the menu leaves
// the grid describing the files it actually holds.
//
// The set may span families. Archive numbers are not comparable across them,
// which is why the family selector, file list and Recycle each still mean one
// family, but a merged read is per-ref and a row carries its own logFileRef, so
// a mixed set costs only the family in the labels. Reading the SQL Server and
// Agent logs of the same minute side by side is the reason to look at the Agent
// log at all.
func (lv *LogViewer) showLogFileChecklist() {
	families := lv.enumeratedFamilies()
	if len(families) == 0 {
		lv.popMenu(logToolFile, []controls.MenuItem{
			{Label: "(log list not loaded — Refresh)", Action: lv.Refresh},
		})
		return
	}
	// Named only when there is something to tell apart: with one family
	// enumerated, rows stay unprefixed.
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
			// The item's own index, taken as appended: family dividers make it differ
			// from the file's list position, and SetHover addresses menu rows.
			row := len(items)
			items = append(items, controls.MenuItem{Label: mark + label, Action: func() {
				if j := slices.Index(lv.pending, ref); j >= 0 {
					lv.pending = slices.Delete(lv.pending, j, j+1)
				} else {
					lv.pending = append(lv.pending, ref)
				}
				lv.showLogFileChecklist()
				// Re-showing resets the hover, which sends a keyboard user's cursor back to
				// the top on every tick. Restore it to the ticked row.
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
// count is visible before the menu closes. named carries the single-file rule:
// the family is part of the name only while more than one is on offer.
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
// didn't fit.
func (lv *LogViewer) popMenu(i int, items []controls.MenuItem) {
	r := lv.tools.Cells[i].Rect
	if r.IsZero() {
		r = core.Rect{X: lv.rect.X, Y: lv.rect.Y}
	}
	lv.app.contextMenu.Show(r.X, r.Y+1, items)
}

// export writes the entries currently shown (the filtered set, not the whole
// file) to a tab-separated file.
func (lv *LogViewer) export() {
	if len(lv.shown) == 0 {
		lv.app.setStatus("Nothing to export")
		return
	}
	family := strings.ToLower(strings.ReplaceAll(lv.logType.String(), " ", "-"))
	name := fmt.Sprintf("%s-log-%d.txt", family, lv.currentRef().Num)
	switch {
	case lv.multiFamily():
		// The family in the name would be the selectors' one, which is not what the
		// file holds.
		name = fmt.Sprintf("error-log-%dfiles.txt", len(lv.sel))
	case lv.multiFile():
		name = fmt.Sprintf("%s-log-%dfiles.txt", family, len(lv.sel))
	}
	lv.app.fileDialog.ShowSave("Export Log", name, func(path string) {
		// Rendered on the UI goroutine, with only the write off it: applyFilter reuses
		// shown's backing array, so a slice snapshot would be rewritten under the
		// goroutine by the next filter keystroke.
		text := lv.exportText()
		n := len(lv.shown)
		lv.app.safego("exporting a log", func() {
			// On the UI goroutine this would freeze the app: a big log to a network path
			// takes seconds.
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
	b.WriteString(strings.Join(lv.exportColumns(), "\t"))
	b.WriteString("\n")
	for _, r := range lv.shown {
		b.WriteString(strings.Join(lv.cells(r), "\t"))
		b.WriteString("\n")
	}
	return b.String()
}

// recycleLogFrom cycles a log family from its Object Explorer folder, then
// refreshes the folder so the renumbered archives appear.
//
// Any LogViewer open on the same connection is refreshed too, via Refresh not
// Load: the viewer may be on the *other* family, and Load re-enumerates only
// the family on screen, so the cycled one's cached numbering would survive and
// name the wrong archive.
func (a *App) recycleLogFrom(sc *db.ServerConn, logType gosmo.ErrorLogType, node *explorerNode) {
	if !a.requireConn(sc) {
		return
	}
	a.confirmDialog.ShowConfirm("Recycle Log", cycleLogMessage(logType, sc.Opts.Server), func(confirmed bool) {
		if !confirmed {
			return
		}
		a.runWithProgress(progressJob{
			title:   "Recycle Log",
			message: fmt.Sprintf("Recycling the %s error log...", logType),
			what:    "cycling an error log",
			sc:      sc,
			timeout: logReadTimeout,
		}, func(ctx context.Context, _ progressReport) error {
			return sc.Server.CycleLog(ctx, logType)
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				// Reloaded anyway: the cycle may have landed before the cancel reached the
				// server.
				a.setStatus(fmt.Sprintf("Recycling the %s error log cancelled", logType))
			case err != nil:
				a.setStatus(fmt.Sprintf("Recycle failed: %v", withPermissionAdvice(err)))
				return
			default:
				a.setStatus(fmt.Sprintf("%s error log recycled", logType))
			}
			a.explorer.ReloadFolders(sc, sameNodeAs(node))
			a.refreshOpenLogViewer(sc, logType)
		})
	})
}

// refreshOpenLogViewer re-reads the LogViewer open on sc, if any, after
// logType was cycled. There is at most one per connection (showLogViewerFor).
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
