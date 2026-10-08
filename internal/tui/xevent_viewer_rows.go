package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_rows.go is what the store becomes on screen: the grid's
// columns and rows, a read's batch applied to both, the summary line, and the
// actions on one event — open its SQL, its plan, its XML. The reads are in
// xevent_viewer_feed.go.

// xeMaxCellRunes is how much of a value a grid cell renders. A showplan or a
// batch runs to megabytes; the grid shows a column's width of it, and
// flattening the whole value for every drawn row would be the panel's cost.
// The details pane, Show Value and Export use the whole value (D4).
const xeMaxCellRunes = 256

// xeSQLFields are the fields holding statement text, which the cell menu
// offers to open in a query window.
var xeSQLFields = []string{"sql_text", "batch_text", "statement"}

// xeRowSource is the grid's view of the panel: shown, in the current columns,
// or while grouped the display rows. Read on every draw, so appending to
// shown grows the grid in place.
type xeRowSource struct{ v *XEventViewer }

func (s xeRowSource) Len() int {
	if s.v.grouped() {
		return len(s.v.display)
	}
	return len(s.v.shown)
}

func (s xeRowSource) Row(i int) []string {
	if g, ok := s.v.groupAt(i); ok {
		return s.v.groupCells(g)
	}
	e, ok := s.v.eventAt(i)
	if !ok {
		return nil
	}
	cells := make([]string, len(s.v.columns))
	for j, c := range s.v.columns {
		if val, ok := e.Value(c); ok {
			cells[j] = xeCellText(val.Display())
		}
	}
	return cells
}

// RowKind sets group rows and bookmarked events apart
// (controls.RowKindSource).
func (s xeRowSource) RowKind(i int) controls.RowKind {
	if _, ok := s.v.groupAt(i); ok {
		return controls.RowGroup
	}
	if e, ok := s.v.eventAt(i); ok && s.v.bookmarks[e.ID] {
		return controls.RowMarked
	}
	return controls.RowNormal
}

// eventAt is the event on grid row row: shown's while ungrouped, an expanded
// group's while grouped, none on a group row.
func (v *XEventViewer) eventAt(row int) (*xevent.Event, bool) {
	if v.grouped() {
		if row < 0 || row >= len(v.display) || v.display[row].event == nil {
			return nil, false
		}
		return v.display[row].event, true
	}
	if row < 0 || row >= len(v.shown) {
		return nil, false
	}
	return v.shown[row], true
}

// xeCellText is a value as one grid cell: its first xeMaxCellRunes runes,
// line breaks and tabs folded to spaces.
func xeCellText(s string) string {
	if len(s) > xeMaxCellRunes {
		n := 0
		for i := range s {
			if n == xeMaxCellRunes {
				s = s[:i] + "…"
				break
			}
			n++
		}
	}
	return flattenLogText(s)
}

// allColumns is every column the events have carried, in the store's order.
func (v *XEventViewer) allColumns() []xevent.Column { return v.store.Columns() }

// visibleColumns is allColumns less the hidden ones — never empty: with
// everything hidden (a config written by hand) the name stays.
func (v *XEventViewer) visibleColumns() []xevent.Column {
	var out []xevent.Column
	for _, c := range v.allColumns() {
		if !v.hiddenCols[xeColumnKey(c)] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []xevent.Column{xevent.NameColumn}
	}
	return out
}

// headers names the current columns for the grid and the export.
func (v *XEventViewer) headers() []string {
	out := make([]string, len(v.columns))
	for i, c := range v.columns {
		out[i] = xevent.Header(c, v.columns)
	}
	return out
}

// rebuildRows rebuilds shown from the whole store and hands the grid the new
// column set, keeping its cursor and scroll: after a filter change, a column
// change, a Clear or a Resume.
func (v *XEventViewer) rebuildRows() {
	v.feed.overlayHeld = false
	v.columns = v.visibleColumns()
	v.shown = v.shown[:0:0]
	for i := range v.store.Len() {
		if e := v.store.At(i); v.filter.Match(e) {
			v.shown = append(v.shown, e)
		}
	}
	if v.grouped() {
		v.regroup()
		v.updateStatus()
		return
	}
	v.invalidateDetailCache()
	v.grid.SetSourcePreservingView(v.headers(), xeRowSource{v})
	v.followTail(true)
	v.updateStatus()
}

// applyBatch takes one read's events into the store and, unless the grid is
// paused, onto its end.
func (v *XEventViewer) applyBatch(b xeBatch) {
	v.feed.source = b.source
	v.feed.missing += b.missing
	v.feed.truncated = v.feed.truncated || b.truncated
	v.feed.unsequenced = b.unsequenced
	v.feed.progress = b.progress
	v.feed.skipped += b.skipped
	v.feed.rolledOver = v.feed.rolledOver || b.rolledOver
	v.feed.discarded += b.discarded
	v.feed.err = nil
	if len(b.events) == 0 {
		v.updateStatus()
		return
	}
	added, grew := v.store.Add(b.events...)
	if v.feed.paused {
		v.feed.pending += len(added)
		v.updateStatus()
		return
	}
	atEnd := len(v.shown) == 0 || v.grid.SelectedRow() == len(v.shown)-1
	before := len(v.shown)
	for _, e := range added {
		if v.filter.Match(e) {
			v.shown = append(v.shown, e)
		}
	}
	if v.grouped() {
		if v.grid.OverlayActive() {
			v.feed.overlayHeld = true
			v.updateStatus()
			return
		}
		// Any group can have grown, and one can have appeared above the
		// cursor: regrouped whole, the cursor kept on what it was on.
		v.dropAged()
		if grew {
			v.columns = v.visibleColumns()
		}
		v.regroup()
		v.updateStatus()
		return
	}
	v.trimShown()
	switch {
	case grew && v.grid.OverlayActive():
		v.feed.overlayHeld = true
	case grew:
		v.columns = v.visibleColumns()
		v.grid.SetSourcePreservingView(v.headers(), xeRowSource{v})
	case before < 200:
		// Column widths are sized from the first rows; past those, a new row
		// changes nothing.
		v.grid.RefreshColumnWidths()
	}
	v.followTail(atEnd)
	v.updateStatus()
}

// catchUpAfterOverlay does the grid rebuild applyBatch put off while the
// grid's overlay was open, once it has closed.
func (v *XEventViewer) catchUpAfterOverlay() {
	if !v.feed.overlayHeld || v.grid.OverlayActive() {
		return
	}
	v.feed.overlayHeld = false
	v.columns = v.visibleColumns()
	if v.grouped() {
		v.dropAged()
		v.regroup()
	} else {
		v.grid.SetSourcePreservingView(v.headers(), xeRowSource{v})
	}
	v.updateStatus()
}

// followTail keeps the newest event selected while Auto Scroll is on and the
// cursor was on the last row: moving up to read an event stops the following,
// going back to the bottom resumes it.
func (v *XEventViewer) followTail(atEnd bool) {
	if v.feed.autoScroll && atEnd && len(v.shown) > 0 {
		_, col := v.grid.SelectedCell()
		v.grid.SetSelectedCell(len(v.shown)-1, col)
	}
}

// trimShown drops from the front of shown the events the store has pushed
// out, moving the grid's cursor and scroll up with the rows so they stay on
// the event they were on.
func (v *XEventViewer) trimShown() {
	row, col := v.grid.SelectedCell()
	scroll := v.grid.ScrollRow()
	k := v.dropAged()
	if k == 0 {
		return
	}
	v.grid.ClearMarkedRows()
	v.grid.SetScroll(scroll-k, v.grid.ScrollCol())
	v.grid.SetSelectedCell(max(0, row-k), col)
}

// dropAged drops from the front of shown, and from the bookmarks, the events
// the store has pushed out, and says how many shown lost.
func (v *XEventViewer) dropAged() int {
	oldest := v.store.OldestID()
	for id := range v.bookmarks {
		if id < oldest {
			delete(v.bookmarks, id)
		}
	}
	k, _ := slices.BinarySearchFunc(v.shown, oldest, func(e *xevent.Event, id uint64) int {
		switch {
		case e.ID < id:
			return -1
		case e.ID > id:
			return 1
		}
		return 0
	})
	v.shown = v.shown[k:]
	return k
}

// updateStatus writes the summary into the grid's status bar.
func (v *XEventViewer) updateStatus() { v.grid.SetStatus(v.summary()) }

// summary is the status line: the feed's state, how many events are held and
// shown, and every way the grid can be short of what the session collected —
// the store full, a ring_buffer wrapped between reads or truncated, the
// dedupe comparing content. Each is said, since none is visible otherwise.
func (v *XEventViewer) summary() string {
	var state string
	switch {
	case v.feed.connecting:
		state = "Connecting..."
	case v.feed.connErr != nil:
		state = "Connection failed: " + firstErrorLine(v.feed.connErr.Error())
	case v.feed.err != nil:
		state = "Stopped — " + firstErrorLine(displayError(v.feed.err).Error())
	case v.feed.paused:
		state = "Paused"
	case v.feed.running && v.live:
		state = "Live"
	case v.feed.running:
		state = "Reading..."
	case v.live:
		state = "Data feed stopped"
	default:
		state = "Read"
	}
	if v.feed.source != "" {
		state += " (" + v.feed.source + ")"
	}
	parts := []string{state}
	if v.feed.progress != "" {
		parts = append(parts, v.feed.progress)
	}
	if v.feed.paused {
		parts = append(parts, fmt.Sprintf("%d new since the pause", v.feed.pending))
	}
	if v.filter != nil {
		parts = append(parts, fmt.Sprintf("%d of %d events match the filter", len(v.shown), v.store.Len()))
	} else {
		parts = append(parts, fmt.Sprintf("%d event%s", v.store.Len(), pluralSuffix(v.store.Len())))
	}
	if v.grouped() {
		parts = append(parts, fmt.Sprintf("%d group%s", len(v.groups), pluralSuffix(len(v.groups))))
	}
	if n := len(v.bookmarks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d bookmarked", n))
	}
	if d := v.store.Dropped(); d > 0 {
		parts = append(parts, fmt.Sprintf("%d oldest dropped (holds %d)", d, v.store.Capacity()))
	}
	if v.feed.discarded > 0 {
		parts = append(parts, fmt.Sprintf("%d older events not kept (the viewer holds %d)", v.feed.discarded, v.store.Capacity()))
	}
	if v.feed.skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d older files not read (the viewer holds %d events)", v.feed.skipped, v.store.Capacity()))
	}
	if v.feed.rolledOver {
		parts = append(parts, "rollover deleted a file before it was read — resumed at the oldest file left; events may be missing")
	}
	if v.feed.missing > 0 {
		parts = append(parts, fmt.Sprintf("buffer wrapped — %d events may be missing", v.feed.missing))
	}
	if v.feed.truncated {
		parts = append(parts, "ring_buffer output truncated — the oldest events were not returned")
	}
	if v.feed.unsequenced {
		parts = append(parts, "no event_sequence — repeats told apart by content")
	}
	return strings.Join(parts, " — ")
}

// selectedEvent is the event under the grid's cursor.
func (v *XEventViewer) selectedEvent() (*xevent.Event, bool) {
	return v.eventAt(v.grid.SelectedRow())
}

// selectedColumn is the column under the grid's cursor.
func (v *XEventViewer) selectedColumn() (xevent.Column, bool) {
	_, col := v.grid.SelectedCell()
	if col < 0 || col >= len(v.columns) {
		return xevent.Column{}, false
	}
	return v.columns[col], true
}

// showValue is the grid's Show Value, given the whole value rather than the
// cell's cut of it: statement text in a SQL panel, a plan in the plan viewer,
// XML in an XML panel, anything the cell cut short in a text panel. What
// fitted in its cell is left to the grid's own popup.
func (v *XEventViewer) showValue(col int, _ string, _ string) bool {
	e, ok := v.selectedEvent()
	if !ok || col < 0 || col >= len(v.columns) {
		return false
	}
	c := v.columns[col]
	val, ok := e.Value(c)
	if !ok {
		return false
	}
	switch {
	case c.Name == "showplan_xml":
		return v.openPlan(e)
	case val.IsXML:
		return v.app.openValuePanel(c.Name, ".xml", controls.XMLHighlighter(theme.Active()), val.Value)
	case slices.Contains(xeSQLFields, c.Name):
		return v.app.openValuePanel(c.Name, ".sql", controls.SQLHighlighter(theme.Active()), val.Display())
	case xeCellText(val.Display()) != val.Display():
		return v.app.openValuePanel(c.Name, ".txt", nil, val.Display())
	}
	return false
}

// cellMenuItems are the grid's right-click entries for the selected event:
// its SQL in a query window, its plan, its XML, the cell as a filter, and the
// whole event as text.
func (v *XEventViewer) cellMenuItems() []controls.MenuItem {
	if g, ok := v.groupAt(v.grid.SelectedRow()); ok {
		return v.groupMenuItems(g)
	}
	e, ok := v.selectedEvent()
	if !ok {
		return nil
	}
	var items []controls.MenuItem
	for _, name := range xeSQLFields {
		if val, ok := e.Field(name); ok && strings.TrimSpace(val.Value) != "" {
			text := val.Value
			items = append(items, controls.MenuItem{Label: "Open " + name + " in Query Window",
				Action: func() { v.openInQueryWindow(e, text) }})
		}
	}
	if _, ok := e.Field("showplan_xml"); ok {
		items = append(items, controls.MenuItem{Label: "Show Plan", Action: func() { v.openPlan(e) }})
	}
	for _, val := range slices.Concat(e.Fields, e.Actions) {
		if val.IsXML && val.Name != "showplan_xml" {
			items = append(items, controls.MenuItem{Label: "Open " + val.Name + " as XML",
				Action: func() { v.app.openValuePanel(val.Name, ".xml", controls.XMLHighlighter(theme.Active()), val.Value) }})
		}
	}
	if c, ok := v.selectedColumn(); ok {
		if val, ok := e.Value(c); ok {
			items = append(items, controls.MenuItem{Label: "Filter by This Value",
				Action: func() { v.filterByValue(c, val.Display()) }})
		}
		label := "Group by This Column"
		if slices.Contains(v.groupBy, c) {
			label = "Ungroup This Column"
		}
		items = append(items, controls.MenuItem{Label: label, Action: func() { v.toggleGroupBy(c) }})
	}
	label := "Bookmark This Event"
	if v.bookmarks[e.ID] {
		label = "Remove Bookmark"
	}
	items = append(items,
		controls.MenuItem{Label: label, Shortcut: "Ctrl+F2", Action: v.toggleBookmark},
		controls.MenuItem{Divider: true},
		controls.MenuItem{Label: "Copy Event Details", Action: func() { v.copyEventDetails() }},
		controls.MenuItem{Label: "Copy Rows with Headers", Action: func() { v.copyRows() }})
	return items
}

// selectedEvents are the events of the grid's selected rows, in grid order;
// group rows in the selection are passed over.
func (v *XEventViewer) selectedEvents() []*xevent.Event {
	var out []*xevent.Event
	for _, r := range v.grid.SelectedRows() {
		if e, ok := v.eventAt(r); ok {
			out = append(out, e)
		}
	}
	return out
}

// copyEventDetails copies the selected events as the details pane shows them,
// whole and unwrapped, a blank line between events.
func (v *XEventViewer) copyEventDetails() {
	var blocks []string
	for _, e := range v.selectedEvents() {
		blocks = append(blocks, strings.Join(v.eventLines(e, 0), "\n"))
	}
	if len(blocks) == 0 {
		v.app.setStatus("No event selected")
		return
	}
	v.app.copyWithStatus(strings.Join(blocks, "\n\n"))
}

// copyRows copies the selected events as tab-separated rows under the
// headers, values whole — the grid's own Copy takes the cells as cut to their
// width.
func (v *XEventViewer) copyRows() {
	evs := v.selectedEvents()
	if len(evs) == 0 {
		v.app.setStatus("No event selected")
		return
	}
	lines := []string{strings.Join(v.headers(), "\t")}
	for _, e := range evs {
		cells := make([]string, len(v.columns))
		for i, c := range v.columns {
			if val, ok := e.Value(c); ok {
				cells[i] = flattenLogText(val.Display())
			}
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	v.app.copyWithStatus(strings.Join(lines, "\n"))
}

// openInQueryWindow opens statement text in a new query panel on the viewer's
// server, in the event's database when it carries one.
func (v *XEventViewer) openInQueryWindow(e *xevent.Event, text string) {
	if !v.app.requireConn(v.host) {
		return
	}
	database := ""
	if val, ok := e.Action("database_name"); ok {
		database = val.Value
	} else if val, ok := e.Field("database_name"); ok {
		database = val.Value
	}
	v.app.openQueryWithText(v.host, database, text)
}

// openPlan shows the event's showplan_xml in the plan viewer.
func (v *XEventViewer) openPlan(e *xevent.Event) bool {
	val, ok := e.Field("showplan_xml")
	if !ok || strings.TrimSpace(val.Value) == "" {
		v.app.setStatus("This event carries no plan")
		return true
	}
	plan, err := showplan.Parse([]byte(val.Value))
	if err != nil {
		v.app.setStatus("Could not parse the plan: " + err.Error())
		return true
	}
	v.app.openPlanPanel(e.Name+" plan", plan)
	return true
}
