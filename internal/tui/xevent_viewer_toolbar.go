package tui

import (
	"fmt"
	"slices"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_toolbar.go is the Extended Events viewer's toolbar: its cells
// for each mode, when each is inert, and the menus and dialogs they open —
// Filter, Choose Columns and Export. The panel is in xevent_viewer.go.
//
// Grouping and Aggregation are in xevent_viewer_group.go, Find and Bookmarks in
// xevent_viewer_find.go, Export and Settings in xevent_viewer_export.go; this
// file only places their cells.

// xeTool names a toolbar cell. The cells differ by mode, so they are looked up
// by name rather than addressed by a fixed index.
type xeTool int

const (
	xeToolFeed xeTool = iota // Stop / Start Data Feed (live)
	xeToolPause
	xeToolClear
	xeToolRefresh // View Target Data's re-read
	xeToolFilter
	xeToolColumns
	xeToolGrouping
	xeToolAggregation
	xeToolAutoScroll
	xeToolFind
	xeToolBookmarks
	xeToolSettings
	xeToolExport
)

// buildTools defines the toolbar for the panel's mode. xeToolIDs parallels
// tools; refreshToolLabels rewrites the labels that carry state.
func (v *XEventViewer) buildTools() {
	var ids []xeTool
	if v.live {
		ids = []xeTool{xeToolFeed, xeToolPause, xeToolClear, xeToolFilter, xeToolColumns,
			xeToolGrouping, xeToolAggregation, xeToolAutoScroll, xeToolFind, xeToolBookmarks,
			xeToolSettings, xeToolExport}
	} else {
		ids = []xeTool{xeToolRefresh, xeToolFilter, xeToolColumns, xeToolGrouping, xeToolAggregation,
			xeToolFind, xeToolBookmarks, xeToolSettings, xeToolExport}
	}
	v.toolIDs = ids
	v.tools.Cells = make([]controls.ToolCell, len(ids))
	for i, id := range ids {
		v.tools.Cells[i].Action = v.toolAction(id)
	}
	v.refreshToolLabels()
}

func (v *XEventViewer) toolAction(id xeTool) func() {
	switch id {
	case xeToolFeed:
		return v.toggleFeed
	case xeToolPause:
		return v.togglePause
	case xeToolClear:
		return v.clearData
	case xeToolRefresh:
		return v.refreshTarget
	case xeToolFilter:
		return v.showFilterMenu
	case xeToolColumns:
		return v.showColumnsMenu
	case xeToolGrouping:
		return v.showGroupingMenu
	case xeToolAggregation:
		return v.showAggregationMenu
	case xeToolAutoScroll:
		return v.toggleAutoScroll
	case xeToolFind:
		return v.showFind
	case xeToolBookmarks:
		return v.showBookmarksMenu
	case xeToolSettings:
		return v.showSettingsMenu
	case xeToolExport:
		return v.showExportMenu
	}
	return func() {}
}

// refreshToolLabels writes each cell's label from the panel's state: the feed
// cell says what pressing it will do, the auto-scroll cell whether it is on.
func (v *XEventViewer) refreshToolLabels() {
	for i, id := range v.toolIDs {
		var label string
		switch id {
		case xeToolFeed:
			label = "Stop Data Feed"
			if !v.feed.running {
				label = "Start Data Feed"
			}
		case xeToolPause:
			label = "Pause"
			if v.feed.paused {
				label = "Resume"
			}
		case xeToolClear:
			label = "Clear Data"
		case xeToolRefresh:
			label = "Refresh"
		case xeToolFilter:
			label = "Filter ▾"
			if v.filter != nil {
				label = "Filter (on) ▾"
			}
		case xeToolColumns:
			label = "Columns ▾"
		case xeToolGrouping:
			label = "Grouping ▾"
			if v.grouped() {
				label = "Grouping (on) ▾"
			}
		case xeToolAggregation:
			label = "Aggregation ▾"
		case xeToolFind:
			label = "Find..."
		case xeToolBookmarks:
			label = "Bookmarks ▾"
		case xeToolSettings:
			label = "Settings ▾"
		case xeToolAutoScroll:
			label = "☐ Auto Scroll"
			if v.feed.autoScroll {
				label = "☑ Auto Scroll"
			}
		case xeToolExport:
			label = "Export ▾"
		}
		v.tools.Cells[i].Label = label
	}
}

// toolIndex is the cell index of id, or -1 when this mode has no such cell.
func (v *XEventViewer) toolIndex(id xeTool) int { return slices.Index(v.toolIDs, id) }

// toolDisabled reports whether cell i is inert right now, and toolReason why.
// Feed-dependent cells need the panel's connection; Refresh waits for the read
// in flight; Pause means nothing while no feed runs.
func (v *XEventViewer) toolDisabled(i int) bool { return v.toolReason(i) != "" }

func (v *XEventViewer) toolReason(i int) string {
	switch v.toolIDs[i] {
	case xeToolFeed:
		if v.conn == nil {
			return "not connected yet"
		}
	case xeToolRefresh:
		if v.conn == nil {
			return "not connected yet"
		}
		if v.feed.running {
			return "reading"
		}
	case xeToolPause:
		if !v.feed.running && !v.feed.paused {
			return "the data feed is stopped"
		}
	case xeToolExport:
		if len(v.shown) == 0 {
			return "no events to export"
		}
	case xeToolAggregation:
		// SSMS's rule: an aggregate is shown on a group row.
		if !v.grouped() {
			return "group the events first"
		}
	case xeToolFind, xeToolBookmarks:
		if len(v.shown) == 0 {
			return "no events"
		}
	}
	return ""
}

// runTool invokes cell i's action, or says why it did not.
func (v *XEventViewer) runTool(i int) bool {
	if i < 0 || i >= len(v.tools.Cells) {
		return false
	}
	if r := v.toolReason(i); r != "" {
		v.app.setStatus(v.tools.Cells[i].Label + ": " + r)
		return false
	}
	v.tools.Cells[i].Action()
	return true
}

// runToolByID runs the cell named id, for a key binding. A mode without the
// cell answers false.
func (v *XEventViewer) runToolByID(id xeTool) bool {
	i := v.toolIndex(id)
	if i < 0 {
		return false
	}
	v.runTool(i)
	return true
}

// showOverflowMenu pops the cells the row was too narrow to draw.
func (v *XEventViewer) showOverflowMenu() {
	r := v.tools.More.Rect
	if r.IsZero() {
		r = core.Rect{X: v.rect.X, Y: v.rect.Y}
	}
	v.app.contextMenu.Show(r.X, r.Y+1,
		v.tools.OverflowItems(v.toolDisabled, v.toolReason, func(i int) { v.runTool(i) }))
}

// popMenu shows items under cell id, or at the panel's top-left when the cell
// is folded into More.
func (v *XEventViewer) popMenu(id xeTool, items []controls.MenuItem) {
	r := core.Rect{X: v.rect.X, Y: v.rect.Y}
	if i := v.toolIndex(id); i >= 0 && !v.tools.Cells[i].Rect.IsZero() {
		r = v.tools.Cells[i].Rect
	}
	v.app.contextMenu.Show(r.X, r.Y+1, items)
}

// toggleAutoScroll turns following the newest event on or off.
func (v *XEventViewer) toggleAutoScroll() {
	v.feed.autoScroll = !v.feed.autoScroll
	// Grouped, the rows are not in arrival order: there is no tail to follow.
	if v.feed.autoScroll && len(v.shown) > 0 && !v.grouped() {
		_, col := v.grid.SelectedCell()
		v.grid.SetSelectedCell(len(v.shown)-1, col)
	}
	v.refreshToolLabels()
}

// -- Filter ---------------------------------------------------------------------

// xeFilterHelp is the Filter dialog's explanation: the whole syntax, since
// there is nowhere else to learn it. Written as sentences because the
// dialog's message wrap folds line breaks.
const xeFilterHelp = "Show only the events matching an expression like duration > 1000 and " +
	"database_name = 'my db', or containing the text typed. Operators: = <> < <= > >=, " +
	"~ (contains), !~, CONTAINS, STARTS WITH, IS NULL, IS NOT NULL; join terms with AND " +
	"and OR. A column is name, timestamp, package, or any field or action (field:x " +
	"or action:x to name one kind). Numbers compare as numbers."

// showFilterMenu offers editing and clearing the filter. Clearing is its own
// entry: the prompt refuses an empty value.
func (v *XEventViewer) showFilterMenu() {
	v.popMenu(xeToolFilter, []controls.MenuItem{
		{Label: "Edit Filter...", Shortcut: "F7", Action: v.editFilter},
		{Label: "Clear Filter", Enabled: func() bool { return v.filter != nil }, Note: "no filter in force",
			Action: func() { v.setFilter(nil) }},
	})
}

// editFilter opens the filter prompt on the expression in force. A value that
// doesn't parse keeps the prompt open with the reason.
func (v *XEventViewer) editFilter() {
	v.app.promptDialog.ShowPrompt("Filter Events", xeFilterHelp, "Filter:", v.filter.String(), func(s string) {
		f, err := xevent.ParseFilter(s)
		if err != nil {
			// Validate already refused it; nothing reaches here unparsed.
			return
		}
		v.setFilter(f)
	})
	v.app.promptDialog.Validate = func(s string) error {
		_, err := xevent.ParseFilter(s)
		return err
	}
}

// setFilter puts f in force and rebuilds the grid from the store.
func (v *XEventViewer) setFilter(f *xevent.Filter) {
	v.filter = f
	v.rebuildRows()
	v.refreshToolLabels()
}

// filterByValue ANDs `column = value` onto the filter in force — the cell
// menu's Filter by This Value. A free-text filter is replaced, since text
// can't be ANDed onto.
func (v *XEventViewer) filterByValue(c xevent.Column, value string) {
	if v.filter.IsFreeText() {
		v.app.setStatus("Replaced the text filter " + fmt.Sprintf("%q", v.filter.String()))
	}
	v.setFilter(v.filter.AndEquals(c.FilterName(), value))
}

// filterByGroup ANDs g's whole path onto the filter in force — one term per
// enclosing group and g's own, `is null` for a "(no value)" group — so the
// grid shows exactly g's events. The text shows every term, to be edited.
func (v *XEventViewer) filterByGroup(g *xevent.Group) {
	if v.filter.IsFreeText() {
		v.app.setStatus("Replaced the text filter " + fmt.Sprintf("%q", v.filter.String()))
	}
	v.setFilter(v.filter.AndTerms(g.Terms(v.allColumns())...))
}

// -- Choose Columns ----------------------------------------------------------

// showColumnsMenu pops the column checklist: one tickable row per column the
// events have carried, applied as it is ticked (hiding a column needs no
// read), then Show All.
func (v *XEventViewer) showColumnsMenu() {
	all := v.allColumns()
	items := make([]controls.MenuItem, 0, len(all)+2)
	for _, c := range all {
		key := xeColumnKey(c)
		mark := "☑ "
		if v.hiddenCols[key] {
			mark = "☐ "
		}
		row := len(items)
		items = append(items, controls.MenuItem{Label: mark + xevent.Header(c, all), Action: func() {
			v.toggleColumn(key)
			v.showColumnsMenu()
			// Re-showing resets the hover; put it back on the row just ticked.
			v.app.contextMenu.SetHover(row)
		}})
	}
	items = append(items,
		controls.MenuItem{Divider: true},
		controls.MenuItem{Label: "Show All", Enabled: func() bool { return len(v.hiddenCols) > 0 }, Note: "all shown",
			Action: func() {
				v.hiddenCols = map[string]bool{}
				v.columnsChanged()
			}})
	v.popMenu(xeToolColumns, items)
}

// toggleColumn hides or shows the column keyed key. The last visible column
// can't be hidden: a grid of no columns shows nothing and offers no way back.
func (v *XEventViewer) toggleColumn(key string) {
	if v.hiddenCols[key] {
		delete(v.hiddenCols, key)
	} else {
		if len(v.columns) <= 1 {
			v.app.setStatus("At least one column stays visible")
			return
		}
		v.hiddenCols[key] = true
	}
	v.columnsChanged()
}

// columnsChanged rebuilds the grid for the new column choice and saves it
// under the session's name.
func (v *XEventViewer) columnsChanged() {
	v.rebuildRows()
	saveXEHiddenColumns(v.app, v.columnsKey(), v.hiddenCols)
}

// xeColumnKey is how a column is named in config: kind and name, since a
// field and an action can share a name.
func xeColumnKey(c xevent.Column) string { return c.Key() }

// loadXEHiddenColumns reads a session's hidden columns from config.
func loadXEHiddenColumns(app *App, session string) map[string]bool {
	out := map[string]bool{}
	if app.cfg == nil {
		return out
	}
	for _, k := range app.cfg.XEventHiddenColumns[session] {
		out[k] = true
	}
	return out
}

// saveXEHiddenColumns writes a session's hidden columns to config. The map is
// replaced, never written into — see config.Config.XEventHiddenColumns.
func saveXEHiddenColumns(app *App, session string, hidden map[string]bool) {
	if app.cfg == nil {
		return
	}
	next := make(map[string][]string, len(app.cfg.XEventHiddenColumns)+1)
	for k, cols := range app.cfg.XEventHiddenColumns {
		next[k] = cols
	}
	keys := make([]string, 0, len(hidden))
	for k := range hidden {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		delete(next, session)
	} else {
		next[session] = keys
	}
	app.cfg.XEventHiddenColumns = next
	app.saveConfig(func(err error) {
		if err != nil {
			app.setStatus("Column choice not saved: " + err.Error())
		}
	})
}
