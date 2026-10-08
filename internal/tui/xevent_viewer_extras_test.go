package tui

import (
	"context"
	"database/sql/driver"
	"encoding/csv"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// Phase F of the Extended Events viewer: grouping and aggregation as
// collapsible grid rows, Find and bookmarks, the export formats, saved
// display settings, and Merge Extended Event Files' reader.

var xeDuration = xevent.Column{Kind: xevent.ColField, Name: "duration"}

// groupedViewer holds five events over two names, grouped by name.
func groupedViewer(t *testing.T) *XEventViewer {
	t.Helper()
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("rpc", 1, "duration", "10"),
		xeTestEvent("batch", 2, "duration", "20"),
		xeTestEvent("rpc", 3, "duration", "30"),
		xeTestEvent("batch", 4, "duration", "40"),
		xeTestEvent("rpc", 5, "duration", "50"),
	}})
	v.setGrouping([]xevent.Column{xevent.NameColumn})
	return v
}

// gridLabels are the grid's rows as text: a group's label, an event's name.
func gridLabels(v *XEventViewer) []string {
	src := xeRowSource{v}
	out := make([]string, src.Len())
	for i := range out {
		out[i] = src.Row(i)[0]
	}
	return out
}

func rowOf(v *XEventViewer, name string, seq uint64) int {
	for i := range (xeRowSource{v}).Len() {
		if e, ok := v.eventAt(i); ok && e.Name == name && e.Seq == seq {
			return i
		}
	}
	return -1
}

func groupRow(v *XEventViewer, value string) int {
	for i := range (xeRowSource{v}).Len() {
		if g, ok := v.groupAt(i); ok && g.Value == value {
			return i
		}
	}
	return -1
}

func TestXEventGroupingShowsCollapsedGroupsWithCountsAndAggregates(t *testing.T) {
	v := groupedViewer(t)
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggSum, Column: xeDuration})
	got := gridLabels(v)
	want := []string{"▸ name: batch  (2 events)", "▸ name: rpc  (3 events)"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	for i := range got {
		if (xeRowSource{v}).RowKind(i) != controls.RowGroup {
			t.Errorf("row %d is not a group row", i)
		}
	}
	dur := slices.Index(v.columns, xeDuration)
	if cell := groupCell(v, "rpc", xeDuration); cell != "SUM 90" {
		t.Errorf("rpc's duration cell %q, want SUM 90 under the column", cell)
	}
	// A group without the column has no aggregate to show, and says so.
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("attention", 6)}})
	if cell := (xeRowSource{v}).Row(0)[dur]; cell != "SUM —" {
		t.Errorf("empty aggregate: %q", cell)
	}
	if !strings.Contains(v.summary(), "3 groups") {
		t.Errorf("summary %q does not count the groups", v.summary())
	}
	// Aggregation's cell is inert until grouped, and says why.
	v.setGrouping(nil)
	if r := v.toolReason(v.toolIndex(xeToolAggregation)); r == "" {
		t.Error("Aggregation offered with no grouping")
	}
}

// groupCell is the cell of the group row valued value under column c.
func groupCell(v *XEventViewer, value string, c xevent.Column) string {
	return (xeRowSource{v}).Row(groupRow(v, value))[slices.Index(v.columns, c)]
}

// Two aggregates on one column share its cell, in the order asked for; the
// group row has a cell per column, the others empty for the label to spill
// across.
func TestXEventAggregatesOnOneColumnShareItsCell(t *testing.T) {
	v := groupedViewer(t)
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggSum, Column: xeDuration})
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggMax, Column: xeDuration})
	if cell := groupCell(v, "rpc", xeDuration); cell != "SUM 90 · MAX 50" {
		t.Errorf("rpc's duration cell %q, want both aggregates", cell)
	}
	row := (xeRowSource{v}).Row(groupRow(v, "rpc"))
	if len(row) != len(v.columns) {
		t.Fatalf("group row has %d cells for %d columns", len(row), len(v.columns))
	}
	for i, cell := range row[1:] {
		if i+1 != slices.Index(v.columns, xeDuration) && cell != "" {
			t.Errorf("cell %d is %q, want it empty", i+1, cell)
		}
	}
}

// An aggregate with no cell of its own stays in the label: its column hidden,
// or the first column, whose cell the label is.
func TestXEventAggregateWithoutACellStaysInTheLabel(t *testing.T) {
	v := groupedViewer(t)
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggCount, Column: xevent.NameColumn})
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggSum, Column: xeDuration})
	if got := gridLabels(v)[1]; got != "▸ name: rpc  (3 events)  COUNT(name) = 3" {
		t.Errorf("first-column aggregate: label %q", got)
	}
	v.hiddenCols[xeColumnKey(xeDuration)] = true
	v.rebuildRows()
	if got := gridLabels(v)[1]; got != "▸ name: rpc  (3 events)  COUNT(name) = 3  SUM(duration) = 90" {
		t.Errorf("hidden-column aggregate: label %q", got)
	}
	if row := (xeRowSource{v}).Row(groupRow(v, "rpc")); slices.ContainsFunc(row[1:], func(c string) bool { return c != "" }) {
		t.Errorf("hidden-column aggregate still has a cell: %q", row)
	}
}

// Enter on a group opens it under its row; the cursor stays on the group.
// A second level nests under the first.
func TestXEventGroupExpandsAndNests(t *testing.T) {
	v := groupedViewer(t)
	v.grid.SetSelectedRow(groupRow(v, "rpc"))
	v.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if got := gridLabels(v); len(got) != 5 || got[1] != "▾ name: rpc  (3 events)" || got[2] != "rpc" {
		t.Fatalf("after Enter on rpc: %q", got)
	}
	if g, ok := v.groupAt(v.grid.SelectedRow()); !ok || g.Value != "rpc" {
		t.Error("the cursor left the group it opened")
	}
	// Left closes it again, Right reopens it.
	v.HandleKey(tcell.NewEventKey(tcell.KeyLeft, "", tcell.ModNone))
	if len(gridLabels(v)) != 2 {
		t.Errorf("Left did not close the group: %q", gridLabels(v))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	if len(gridLabels(v)) != 5 {
		t.Errorf("Right did not open the group: %q", gridLabels(v))
	}

	v.toggleGroupBy(xeDuration)
	v.expandAll(true)
	got := gridLabels(v)
	if got[0] != "▾ name: batch  (2 events)" || got[1] != "  ▾ duration: 20  (1 event)" || got[2] != "batch" {
		t.Errorf("nested rows %q", got[:3])
	}
}

// A live read landing in a group above the cursor must not move it off the
// event it is on: grouped, rows are found again by identity, not index.
func TestXEventGroupedLiveUpdateKeepsTheSelectedEvent(t *testing.T) {
	v := groupedViewer(t)
	v.expandAll(true)
	v.grid.SetSelectedRow(rowOf(v, "rpc", 3))
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("batch", 6), xeTestEvent("attention", 7)}})
	if e, ok := v.selectedEvent(); !ok || e.Seq != 3 {
		t.Errorf("selected %+v after a read above it, want rpc 3", e)
	}
	if got := gridLabels(v)[0]; got != "▸ name: attention  (1 event)" {
		t.Errorf("new group %q, want it collapsed at the top", got)
	}
	if !strings.Contains(gridLabels(v)[1], "(3 events)") {
		t.Errorf("batch group %q, want 3 events", gridLabels(v)[1])
	}
}

// Find walks events in grid order, opens the collapsed group holding the
// hit, wraps, and steps back with a negative direction.
func TestXEventFindOpensTheGroupAndWraps(t *testing.T) {
	v := groupedViewer(t)
	v.grid.SetSelectedRow(0)
	v.find = "30"
	v.findNext(1)
	if e, ok := v.selectedEvent(); !ok || e.Seq != 3 {
		t.Fatalf("found %+v, want rpc 3 (duration 30)", e)
	}
	if !v.expanded[v.groupPath(3)[0].Key] {
		t.Error("the group holding the hit was not opened")
	}
	v.find = "rpc"
	v.findNext(1)
	v.findNext(1)
	if e, _ := v.selectedEvent(); e.Seq != 1 {
		t.Errorf("after two more: rpc %d, want the search wrapped to rpc 1", e.Seq)
	}
	v.findNext(-1)
	if e, _ := v.selectedEvent(); e.Seq != 5 {
		t.Errorf("backwards from rpc 1: rpc %d, want 5", e.Seq)
	}

	// From a group's own row, its first event is the next hit, not skipped.
	v.expandAll(false)
	v.grid.SetSelectedRow(groupRow(v, "rpc"))
	v.findNext(1)
	if e, ok := v.selectedEvent(); !ok || e.Seq != 1 {
		t.Errorf("Find from the rpc group row: %+v, want rpc 1", e)
	}
}

func TestXEventFindUngroupedFromTheCursor(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("a", 1, "t", "x"), xeTestEvent("b", 2, "t", "needle"), xeTestEvent("c", 3, "t", "NEEDLE"),
	}})
	v.grid.SetSelectedRow(1)
	v.find = "needle"
	v.findNext(1)
	if selectedName(v) != "c" {
		t.Errorf("found %q, want c — the next match after the cursor, ignoring case", selectedName(v))
	}
}

func TestXEventBookmarks(t *testing.T) {
	v := newTestXEventViewer(t, 4)
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("a", 1), xeTestEvent("b", 2), xeTestEvent("c", 3)}})
	v.grid.SetSelectedRow(0)
	v.HandleKey(tcell.NewEventKey(tcell.KeyF2, "", tcell.ModCtrl))
	v.grid.SetSelectedRow(2)
	v.toggleBookmark()
	if (xeRowSource{v}).RowKind(0) != controls.RowMarked || (xeRowSource{v}).RowKind(1) != controls.RowNormal {
		t.Error("bookmarked rows not marked")
	}
	v.grid.SetSelectedRow(1)
	v.HandleKey(tcell.NewEventKey(tcell.KeyF2, "", tcell.ModNone))
	if selectedName(v) != "c" {
		t.Errorf("F2 from b: %q, want c", selectedName(v))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyF2, "", tcell.ModNone))
	if selectedName(v) != "a" {
		t.Errorf("F2 from c: %q, want a (wrapped)", selectedName(v))
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyF2, "", tcell.ModShift))
	if selectedName(v) != "c" {
		t.Errorf("Shift+F2 from a: %q, want c", selectedName(v))
	}
	// a ages out: its bookmark goes with it.
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("d", 4), xeTestEvent("e", 5)}})
	if len(v.bookmarks) != 1 {
		t.Errorf("%d bookmarks after a aged out, want 1", len(v.bookmarks))
	}
	v.clearData()
	if len(v.bookmarks) != 0 {
		t.Error("Clear Data kept bookmarks")
	}
}

// CSV keeps values whole — line breaks and commas inside quotes — where the
// tab-separated export folds them.
func TestXEventExportCSV(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("a", 1, "batch_text", "select 1,\n2"), xeTestEvent("b", 2),
	}})
	data, err := v.exportCSV()
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || !slices.Equal(recs[0], []string{"name", "timestamp", "batch_text"}) ||
		recs[1][2] != "select 1,\n2" || recs[2][2] != "" {
		t.Errorf("records %q", recs)
	}
}

func TestXEventInsertScript(t *testing.T) {
	v := newTestXEventViewer(t, 5000)
	var evs []xevent.Event
	for i := range 1001 {
		e := xeTestEvent("rpc", uint64(i%50+1), "duration", "7", "cpu", "1.5", "text", "it's")
		if i == 1 {
			e.Fields = e.Fields[:1] // no cpu, no text: NULLs
		}
		evs = append(evs, e)
	}
	v.applyBatch(xeBatch{events: evs})
	v.session = "my trace"
	s := v.insertScript()
	for _, want := range []string{
		"CREATE TABLE [dbo].[xevents_my_trace] (",
		"[name] nvarchar(3) NULL,",
		"[timestamp] datetime2(6) NULL,",
		"[duration] bigint NULL,",
		"[cpu] float NULL,",
		"[text] nvarchar(4) NULL\n",
		", 7, 1.5, N'it''s')",
		", 7, NULL, NULL)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// 1001 rows: T-SQL's 1000-row VALUES limit splits them in two.
	if n := strings.Count(s, "INSERT INTO [dbo].[xevents_my_trace]"); n != 2 {
		t.Errorf("%d INSERTs, want 2", n)
	}
	for _, bad := range []string{"NaN", "Inf", "0x10", "1e", "-", ".", "1.2.3"} {
		if isSQLNumber(bad) {
			t.Errorf("isSQLNumber(%q)", bad)
		}
	}
	for _, good := range []string{"0", "-12", "3.5", "1e5", "2.5E-3", ".5"} {
		if !isSQLNumber(good) {
			t.Errorf("!isSQLNumber(%q)", good)
		}
	}
}

// Saved settings bring back columns, filter, grouping and aggregates in
// another viewer, by name; an unreadable entry is left out and said.
func TestXEventViewSettingsRoundTrip(t *testing.T) {
	v := groupedViewer(t)
	v.toggleGroupBy(xeDuration)
	v.toggleAggregate(xevent.Aggregate{Func: xevent.AggMax, Column: xeDuration})
	v.toggleColumn("timestamp")
	f, _ := xevent.ParseFilter("duration > 15")
	v.setFilter(f)
	s := v.currentViewSetting()
	v.storeViewSetting("slow", &s)
	waitForSaves(t, v.app)

	saved := config.Load().XEventViewSettings["slow"]
	if !slices.Equal(saved.GroupBy, []string{"name", "field:duration"}) || saved.Filter != "duration > 15" ||
		!slices.Equal(saved.Aggregates, []string{"MAX:field:duration"}) || !slices.Equal(saved.Hidden, []string{"timestamp"}) {
		t.Fatalf("saved %+v", saved)
	}

	w := newTestXEventViewer(t, 100)
	w.app = v.app
	w.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("rpc", 1, "duration", "10"), xeTestEvent("rpc", 2, "duration", "20")}})
	saved.GroupBy = append(saved.GroupBy, "bogus")
	w.applyViewSetting("slow", saved)
	if !slices.Equal(w.groupBy, []xevent.Column{xevent.NameColumn, xeDuration}) || len(w.aggs) != 1 ||
		w.filter.String() != "duration > 15" || !w.hiddenCols["timestamp"] {
		t.Errorf("applied: groupBy %v aggs %v filter %q hidden %v", w.groupBy, w.aggs, w.filter.String(), w.hiddenCols)
	}
	if len(w.shown) != 1 || len(w.groups) != 1 {
		t.Errorf("filter not in force: %d shown, %d groups", len(w.shown), len(w.groups))
	}
	v.storeViewSetting("slow", nil)
	waitForSaves(t, v.app)
	if _, ok := config.Load().XEventViewSettings["slow"]; ok {
		t.Error("Delete left the setting")
	}
}

// Merge reads every listed file, then posts the events once, in timestamp
// order across the files.
func TestXEventMergeReadsEveryFileAndSortsByTime(t *testing.T) {
	const fa, fb = `C:\log\a_0_1.xel`, `C:\log\b_0_1.xel`
	dmf := func(path string) []driver.Value {
		return []driver.Value{path, path[len(`C:\log\`):], int64(0), int64(1), time.Time{}}
	}
	sc, inst := newFakeConn(t, slices.Concat([]fakeResponse{
		{match: "ErrorLogFileName", cols: 1, rows: [][]driver.Value{{`C:\log\ERRORLOG`}}},
		{match: "sys.dm_os_enumerate_filesystem", cols: 5, rows: [][]driver.Value{dmf(fb), dmf(fa)}},
		fileRead(fa, 1, 4), fileRead(fb, 2, 3),
	}, xeSessionResponses())...)
	r := &xeReader{sc: sc, files: "*.xel", capacity: 100}
	var got []uint64
	posts := 0
	if err := r.run(context.Background(), func(b xeBatch) {
		if len(b.events) > 0 {
			posts++
		}
		for _, e := range b.events {
			got = append(got, e.Seq)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []uint64{1, 2, 3, 4}) || posts != 1 {
		t.Errorf("events %v in %d posts, want [1 2 3 4] in one", got, posts)
	}
	if fileArgs(inst, fa) == nil || fileArgs(inst, fb) == nil {
		t.Error("a listed file was not read by its path")
	}
	if n := len(inst.StatementsIn("")); n != 0 {
		t.Errorf("a merge wrote %d statements", n)
	}
}

// Past twice the capacity a merge keeps only the newest capacity and counts
// the rest.
func TestXEventMergeKeepsTheNewest(t *testing.T) {
	evs := []xevent.Event{xeTestEvent("c", 3), xeTestEvent("a", 1), xeTestEvent("b", 2)}
	got := newestByTime(evs, 2)
	if len(got) != 2 || got[0].Name != "b" || got[1].Name != "c" {
		t.Errorf("kept %v", got)
	}
}

func TestExtendedEventsFolderOffersMerge(t *testing.T) {
	items := extendedEventsMenuItems(newTestApp(), nil, &explorerNode{}, controls.MenuItem{Label: "New Query"}, controls.MenuItem{Label: "Refresh"})
	if !slices.ContainsFunc(items, func(it controls.MenuItem) bool { return it.Label == "Merge Extended Event Files..." }) {
		t.Error("no Merge Extended Event Files on the Extended Events folder")
	}
}

// The cursor on a group row stays on that group when a new group appears
// above it, not on the row index — which is now its neighbour.
func TestXEventGroupedLiveUpdateKeepsTheSelectedGroup(t *testing.T) {
	v := groupedViewer(t)
	v.grid.SetSelectedRow(groupRow(v, "rpc"))
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("attention", 7)}})
	if g, ok := v.groupAt(v.grid.SelectedRow()); !ok || g.Value != "rpc" {
		t.Errorf("selected %+v after a group appeared above, want the rpc group", g)
	}
}

// Filter by This Group on an inner group filters by the whole path, so it
// shows exactly the group's events — and works on a "(no value)" group.
func TestXEventFilterByThisGroupMatchesTheWholePath(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("rpc", 1, "db", "master"),
		xeTestEvent("batch", 2, "db", "master"),
		xeTestEvent("rpc", 3, "db", "app"),
		xeTestEvent("rpc", 4, "db", "master"),
		xeTestEvent("batch", 5),
		xeTestEvent("rpc", 6),
	}})
	db := xevent.Column{Kind: xevent.ColField, Name: "db"}
	v.setGrouping([]xevent.Column{xevent.NameColumn, db})
	v.expandAll(true)
	find := func(outer, inner string, null bool) *xevent.Group {
		for i := range (xeRowSource{v}).Len() {
			if g, ok := v.groupAt(i); ok && g.Parent != nil && g.Parent.Value == outer && g.Value == inner && g.Null == null {
				return g
			}
		}
		t.Fatalf("no group %s/%s", outer, inner)
		return nil
	}

	g := find("rpc", "master", false)
	for _, it := range v.groupMenuItems(g) {
		if it.Label == "Filter by This Group" {
			it.Action()
		}
	}
	if len(v.shown) != g.Count || len(v.shown) != 2 || v.shown[0].Seq != 1 || v.shown[1].Seq != 4 {
		t.Errorf("shown %v, want rpc 1 and 4", shownNames(v))
	}
	if v.filter.String() != "name = rpc and db = master" {
		t.Errorf("filter text %q", v.filter.String())
	}

	v.setFilter(nil)
	g = find("batch", "", true)
	var item controls.MenuItem
	for _, it := range v.groupMenuItems(g) {
		if it.Label == "Filter by This Group" {
			item = it
		}
	}
	if item.Enabled != nil && !item.Enabled() {
		t.Fatal("Filter by This Group disabled on a (no value) group")
	}
	item.Action()
	if len(v.shown) != 1 || v.shown[0].Seq != 5 {
		t.Errorf("shown %v, want batch 5 alone", shownNames(v))
	}
	if v.filter.String() != "name = batch and db is null" {
		t.Errorf("filter text %q", v.filter.String())
	}
}

// The status line counts one event and one group in the singular — found
// live as "1 groups" when a filter left a single group.
func TestXEventSummaryCountsOneInTheSingular(t *testing.T) {
	v := groupedViewer(t)
	v.filterByValue(xevent.NameColumn, "rpc")
	if s := v.summary(); !strings.HasSuffix(s, "— 1 group") {
		t.Errorf("summary %q, want it to end in \"— 1 group\"", s)
	}
	one := newTestXEventViewer(t, 100)
	one.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("rpc", 1)}})
	if s := one.summary(); !strings.HasSuffix(s, "— 1 event") {
		t.Errorf("summary %q, want it to end in \"— 1 event\"", s)
	}
}

// U5: grouped, each live batch rebuilt the grid with SetSource, which closes
// its overlay — the Show Value popup a user opened closed within a second.
// The rebuild waits while the overlay is open and lands when it closes.
func TestXEventViewerGroupedBatchWaitsForAnOpenOverlay(t *testing.T) {
	v := groupedViewer(t)
	// Show Value on a group row: the grid's own popup (no event to hand off).
	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	v.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // Copy
	v.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) // Show Value
	v.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	viewerOpen := func() bool { v.grid.SelectAll(); return v.grid.SelectedText() != "" }
	if !viewerOpen() {
		t.Fatal("setup: the Show Value popup did not open")
	}
	before := gridLabels(v)
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("sp", 6, "duration", "60")}})
	if !viewerOpen() {
		t.Fatal("a live batch closed the open Show Value popup")
	}
	if got := gridLabels(v); !slices.Equal(got, before) {
		t.Fatalf("grid rows changed under the open menu: %v, was %v", got, before)
	}
	v.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if v.grid.OverlayActive() {
		t.Fatal("setup: Escape left the popup open")
	}
	if groupRow(v, "sp") < 0 {
		t.Fatalf("the held batch's group never appeared after the menu closed: %v", gridLabels(v))
	}
}

// TestXEventInsertScriptTypesHashesAndWideText pins U4: a uint64 past
// bigint's range is decimal(20,0), not a float that rounds two hashes equal,
// and nvarchar is sized in UTF-16 code units, so an emoji counts twice.
func TestXEventInsertScriptTypesHashesAndWideText(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("rpc", 1, "query_hash", "18446744073709551615", "plan_id", "-3", "note", "ok 😀"),
		xeTestEvent("rpc", 2, "query_hash", "18446744073709551614", "plan_id", "9223372036854775808", "note", "é"),
	}})
	v.session = "s"
	s := v.insertScript()
	for _, want := range []string{
		"[query_hash] decimal(20,0) NULL,",
		"[plan_id] decimal(20,0) NULL,",
		"[note] nvarchar(5) NULL\n",
		", 18446744073709551615, -3, N'ok 😀')",
		", 18446744073709551614, 9223372036854775808, N'é')",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if n := utf16Len("a😀\U0010FFFF"); n != 5 {
		t.Errorf("utf16Len = %d, want 5", n)
	}
}
