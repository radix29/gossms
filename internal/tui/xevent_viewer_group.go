package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_viewer_group.go is the Extended Events viewer's Grouping and
// Aggregation, SSMS's toolbar pair: the events grouped by one or more
// columns, nested in the order chosen, each group a collapsible row carrying
// its event count and the aggregates asked for. The grouping itself is
// internal/xevent's (GroupEvents); this is what it becomes on the grid —
// tuikit's RowGroup rows — and the menus that set it.
//
// Grouped, the grid is rebuilt from the groups on every read rather than
// appended to: a new event can land in any group. The selection is kept on
// the group or event it was on, not on its row index, since a group opening
// above it moves every row below.

// xeDisplayRow is one grid row while grouped: a group's header, or an event of
// an expanded innermost group.
type xeDisplayRow struct {
	group *xevent.Group
	event *xevent.Event
}

// grouped reports whether the grid shows groups.
func (v *XEventViewer) grouped() bool { return len(v.groupBy) > 0 }

// xeNoValue is how a group of the events lacking its column is labelled.
const xeNoValue = "(no value)"

// groupLabel is a group row's text: the indent of its depth, the expand glyph,
// the column and value, the count, then each aggregate that has no cell of its
// own (see aggColumn).
func (v *XEventViewer) groupLabel(g *xevent.Group) string {
	glyph := "▸ "
	if v.expanded[g.Key] {
		glyph = "▾ "
	}
	all := v.allColumns()
	value := g.Value
	if g.Null {
		value = xeNoValue
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("  ", g.Level))
	b.WriteString(glyph)
	b.WriteString(xevent.Header(g.Column, all))
	b.WriteString(": ")
	b.WriteString(xeCellText(value))
	noun := "events"
	if g.Count == 1 {
		noun = "event"
	}
	fmt.Fprintf(&b, "  (%d %s)", g.Count, noun)
	for i, a := range v.aggs {
		if i < len(g.Aggregates) && v.aggColumn(a) < 0 {
			b.WriteString("  ")
			b.WriteString(a.Label(all))
			b.WriteString(" = ")
			b.WriteString(aggText(g.Aggregates[i]))
		}
	}
	return b.String()
}

// groupCells is a group row's cells: the label, then each aggregate under its
// column — several on one column share the cell (SUM 275068 · MAX 44955).
func (v *XEventViewer) groupCells(g *xevent.Group) []string {
	cells := make([]string, max(1, len(v.columns)))
	cells[0] = v.groupLabel(g)
	for i, a := range v.aggs {
		c := v.aggColumn(a)
		if i >= len(g.Aggregates) || c < 0 {
			continue
		}
		if cells[c] != "" {
			cells[c] += " · "
		}
		cells[c] += a.Func.String() + " " + aggText(g.Aggregates[i])
	}
	return cells
}

// aggColumn is the grid column a's value draws under, or -1 when it stays in
// the group label: its column hidden, or the first column, whose cell the
// label is. The rule is fixed rather than measured against the label's
// width, so an aggregate never jumps between the two as columns resize; a
// label longer than the space before the first aggregate cell is clipped
// with "…" there (docs/decisions.md § Extended Events).
func (v *XEventViewer) aggColumn(a xevent.Aggregate) int {
	if c := slices.Index(v.columns, a.Column); c > 0 {
		return c
	}
	return -1
}

// aggText is an aggregate's value as shown: "—" when no event of the group
// has a number there — said, not left blank.
func aggText(val string) string {
	if val == "" {
		return "—"
	}
	return val
}

// regroup rebuilds the groups from shown and the grid's rows from the groups,
// keeping the selection on what it was on.
func (v *XEventViewer) regroup() {
	anchor := v.selectionAnchor()
	v.groups = xevent.GroupEvents(v.shown, v.groupBy, v.aggs)
	v.rebuildDisplay()
	v.restoreSelection(anchor)
}

// rebuildDisplay lays the groups out as rows: each group, and under an
// expanded one its child groups or its events.
func (v *XEventViewer) rebuildDisplay() {
	v.display = v.display[:0:0]
	var walk func(gs []*xevent.Group)
	walk = func(gs []*xevent.Group) {
		for _, g := range gs {
			v.display = append(v.display, xeDisplayRow{group: g})
			if !v.expanded[g.Key] {
				continue
			}
			walk(g.Children)
			for _, e := range g.Events {
				v.display = append(v.display, xeDisplayRow{event: e})
			}
		}
	}
	walk(v.groups)
	v.invalidateDetailCache()
	v.grid.SetSourcePreservingView(v.headers(), xeRowSource{v})
}

// xeAnchor is what the cursor is on, to find again after the rows moved: a
// group by its Key, an event by its ID.
type xeAnchor struct {
	group string
	event uint64
}

func (v *XEventViewer) selectionAnchor() xeAnchor {
	row := v.grid.SelectedRow()
	if g, ok := v.groupAt(row); ok {
		return xeAnchor{group: g.Key}
	}
	if e, ok := v.eventAt(row); ok {
		return xeAnchor{event: e.ID}
	}
	return xeAnchor{}
}

// restoreSelection puts the cursor back on anchor's row. An event now inside
// a collapsed group leaves the cursor on that group — the nearest row still
// showing it; one gone altogether leaves the cursor where the grid kept it.
func (v *XEventViewer) restoreSelection(a xeAnchor) {
	_, col := v.grid.SelectedCell()
	for i, r := range v.display {
		if (a.group != "" && r.group != nil && r.group.Key == a.group) ||
			(a.event != 0 && r.event != nil && r.event.ID == a.event) {
			v.grid.SetSelectedCell(i, col)
			return
		}
	}
	if a.event == 0 {
		return
	}
	for _, g := range slices.Backward(v.groupPath(a.event)) {
		for i, r := range v.display {
			if r.group == g {
				v.grid.SetSelectedCell(i, col)
				return
			}
		}
	}
}

// groupPath is the chain of groups holding the event with id, outermost
// first, or nil when no group holds it.
func (v *XEventViewer) groupPath(id uint64) []*xevent.Group {
	var path []*xevent.Group
	var find func(gs []*xevent.Group) bool
	find = func(gs []*xevent.Group) bool {
		for _, g := range gs {
			path = append(path, g)
			if slices.ContainsFunc(g.Events, func(e *xevent.Event) bool { return e.ID == id }) || find(g.Children) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	if find(v.groups) {
		return path
	}
	return nil
}

// groupAt is the group whose header is grid row row.
func (v *XEventViewer) groupAt(row int) (*xevent.Group, bool) {
	if !v.grouped() || row < 0 || row >= len(v.display) || v.display[row].group == nil {
		return nil, false
	}
	return v.display[row].group, true
}

// setExpanded opens or closes g, leaving the cursor on it.
func (v *XEventViewer) setExpanded(g *xevent.Group, open bool) {
	if open {
		v.expanded[g.Key] = true
	} else {
		delete(v.expanded, g.Key)
	}
	v.rebuildDisplay()
	v.restoreSelection(xeAnchor{group: g.Key})
}

// expandAll opens every group, or closes every one.
func (v *XEventViewer) expandAll(open bool) {
	anchor := v.selectionAnchor()
	v.expanded = map[string]bool{}
	if open {
		var walk func(gs []*xevent.Group)
		walk = func(gs []*xevent.Group) {
			for _, g := range gs {
				v.expanded[g.Key] = true
				walk(g.Children)
			}
		}
		walk(v.groups)
	}
	v.rebuildDisplay()
	v.restoreSelection(anchor)
}

// setGrouping groups by cols, outermost first; none ungroups. The expanded
// set is kept: regrouping by the same columns finds the same Keys.
func (v *XEventViewer) setGrouping(cols []xevent.Column) {
	anchor := v.selectionAnchor()
	v.groupBy = cols
	if !v.grouped() {
		v.groups, v.display = nil, nil
	}
	v.rebuildRows()
	if !v.grouped() && anchor.event != 0 {
		if i := slices.IndexFunc(v.shown, func(e *xevent.Event) bool { return e.ID == anchor.event }); i >= 0 {
			_, col := v.grid.SelectedCell()
			v.grid.SetSelectedCell(i, col)
		}
	}
	v.refreshToolLabels()
}

// toggleGroupBy adds c as the innermost grouping column, or removes it.
func (v *XEventViewer) toggleGroupBy(c xevent.Column) {
	if i := slices.Index(v.groupBy, c); i >= 0 {
		v.setGrouping(slices.Delete(slices.Clone(v.groupBy), i, i+1))
		return
	}
	v.setGrouping(append(slices.Clone(v.groupBy), c))
}

// toggleAggregate adds a to the aggregates, or removes it.
func (v *XEventViewer) toggleAggregate(a xevent.Aggregate) {
	if i := slices.Index(v.aggs, a); i >= 0 {
		v.aggs = slices.Delete(slices.Clone(v.aggs), i, i+1)
	} else {
		v.aggs = append(slices.Clone(v.aggs), a)
	}
	v.rebuildRows()
	v.refreshToolLabels()
}

// showGroupingMenu pops the Grouping checklist: one row per column, ticked
// with its nesting level when grouped by, then Expand All, Collapse All and
// Remove Grouping.
func (v *XEventViewer) showGroupingMenu() {
	all := v.allColumns()
	items := make([]controls.MenuItem, 0, len(all)+5)
	for _, c := range all {
		mark := "☐ "
		if i := slices.Index(v.groupBy, c); i >= 0 {
			mark = "☑ " + strconv.Itoa(i+1) + " "
		}
		row := len(items)
		items = append(items, controls.MenuItem{Label: mark + xevent.Header(c, all), Action: func() {
			v.toggleGroupBy(c)
			v.showGroupingMenu()
			v.app.contextMenu.SetHover(row)
		}})
	}
	grouped := func() bool { return v.grouped() }
	items = append(items,
		controls.MenuItem{Divider: true},
		controls.MenuItem{Label: "Expand All", Enabled: grouped, Note: "not grouped", Action: func() { v.expandAll(true) }},
		controls.MenuItem{Label: "Collapse All", Enabled: grouped, Note: "not grouped", Action: func() { v.expandAll(false) }},
		controls.MenuItem{Label: "Remove Grouping", Enabled: grouped, Note: "not grouped", Action: func() { v.setGrouping(nil) }})
	v.popMenu(xeToolGrouping, items)
}

// showAggregationMenu pops the Aggregation menu: each function over the
// column under the cursor, ticked when in force, then the aggregates in force
// on other columns (untick to remove) and Remove All. Like SSMS, aggregates
// show on group rows, so the cell is inert until the events are grouped.
func (v *XEventViewer) showAggregationMenu() {
	all := v.allColumns()
	var items []controls.MenuItem
	c, ok := v.selectedColumn()
	if ok {
		for _, f := range xevent.AggFuncs {
			a := xevent.Aggregate{Func: f, Column: c}
			items = append(items, v.aggregateItem(a, all))
		}
	}
	var others []controls.MenuItem
	for _, a := range v.aggs {
		if !ok || a.Column != c {
			others = append(others, v.aggregateItem(a, all))
		}
	}
	if len(others) > 0 {
		if len(items) > 0 {
			items = append(items, controls.MenuItem{Divider: true})
		}
		items = append(items, others...)
	}
	if len(items) > 0 {
		items = append(items, controls.MenuItem{Divider: true})
	}
	items = append(items, controls.MenuItem{Label: "Remove All Aggregations",
		Enabled: func() bool { return len(v.aggs) > 0 }, Note: "none in force",
		Action: func() {
			v.aggs = nil
			v.rebuildRows()
			v.refreshToolLabels()
		}})
	v.popMenu(xeToolAggregation, items)
}

func (v *XEventViewer) aggregateItem(a xevent.Aggregate, all []xevent.Column) controls.MenuItem {
	mark := "☐ "
	if slices.Contains(v.aggs, a) {
		mark = "☑ "
	}
	return controls.MenuItem{Label: mark + a.Label(all), Action: func() { v.toggleAggregate(a) }}
}

// groupMenuItems is the cell menu on a group row.
func (v *XEventViewer) groupMenuItems(g *xevent.Group) []controls.MenuItem {
	label := "Expand"
	if v.expanded[g.Key] {
		label = "Collapse"
	}
	return []controls.MenuItem{
		{Label: label, Shortcut: "Enter", Action: func() { v.setExpanded(g, !v.expanded[g.Key]) }},
		{Label: "Expand All", Action: func() { v.expandAll(true) }},
		{Label: "Collapse All", Action: func() { v.expandAll(false) }},
		{Divider: true},
		{Label: "Filter by This Group", Action: func() { v.filterByGroup(g) }},
		{Label: "Remove Grouping", Action: func() { v.setGrouping(nil) }},
	}
}
