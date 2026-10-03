package tui

import (
	"context"
	"database/sql/driver"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/xevent"
)

// The Extended Events viewer: what a read's batch does to the grid (append,
// age out, follow the tail, pause, filter), the Choose Columns state saved per
// session, and the reader's two targets against a scripted server — where a
// live event_file read starts, and what a re-read ring_buffer counts as new
// and as missed.

func xeTestEvent(name string, seq uint64, fields ...string) xevent.Event {
	e := xevent.Event{Name: name, Package: "sqlserver", Seq: seq,
		Timestamp: time.Date(2026, 9, 29, 10, 0, int(seq), 0, time.UTC)}
	for i := 0; i+1 < len(fields); i += 2 {
		e.Fields = append(e.Fields, xevent.Value{Name: fields[i], Value: fields[i+1]})
	}
	return e
}

func newTestXEventViewer(t *testing.T, capacity int) *XEventViewer {
	t.Helper()
	v := NewXEventViewer(newTestApp(), nil, "s", "", true)
	v.store = xevent.NewStore(capacity)
	v.SetBounds(0, 0, 120, 40)
	return v
}

func shownNames(v *XEventViewer) []string {
	out := make([]string, len(v.shown))
	for i, e := range v.shown {
		out[i] = e.Name
	}
	return out
}

func selectedName(v *XEventViewer) string {
	if e, ok := v.selectedEvent(); ok {
		return e.Name
	}
	return ""
}

func TestXEventViewerAppendsAgesOutAndFollowsTheTail(t *testing.T) {
	v := newTestXEventViewer(t, 3)
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("a", 1), xeTestEvent("b", 2)}})
	if got := shownNames(v); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("shown %v", got)
	}
	if selectedName(v) != "b" {
		t.Errorf("auto scroll: selected %q, want the newest, b", selectedName(v))
	}

	// Reading b: the cursor is off the last row, so new events must not
	// move it, and a pushed-out a must not shift it onto another event.
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("c", 3)}})
	v.grid.SetSelectedRow(1) // b, the row above the new tail
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("d", 4), xeTestEvent("e", 5)}})
	if got := shownNames(v); !slices.Equal(got, []string{"c", "d", "e"}) {
		t.Fatalf("after capacity: shown %v, want [c d e]", got)
	}
	if v.store.Dropped() != 2 {
		t.Errorf("Dropped %d, want 2", v.store.Dropped())
	}
	// b itself aged out, so the cursor lands on the oldest held event rather
	// than chasing the tail.
	if selectedName(v) != "c" {
		t.Errorf("selected %q after b aged out, want c", selectedName(v))
	}

	v.grid.SetSelectedRow(len(v.shown) - 1)
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("f", 6)}})
	if selectedName(v) != "f" {
		t.Errorf("back on the last row: selected %q, want f", selectedName(v))
	}
	if !strings.Contains(v.grid.Status(), "3 oldest dropped") {
		t.Errorf("status %q does not say events were dropped", v.grid.Status())
	}
}

// A trimmed front keeps the cursor on the event it was on, not on its row
// number.
func TestXEventViewerTrimKeepsTheCursorOnItsEvent(t *testing.T) {
	v := newTestXEventViewer(t, 4)
	v.feed.autoScroll = false
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("a", 1), xeTestEvent("b", 2), xeTestEvent("c", 3), xeTestEvent("d", 4)}})
	v.grid.SetSelectedRow(2) // c
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("e", 5), xeTestEvent("f", 6)}})
	if selectedName(v) != "c" {
		t.Errorf("selected %q, want c still", selectedName(v))
	}
}

func TestXEventViewerPauseHoldsTheGridAndResumeCatchesUp(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.feed.running = true
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("a", 1)}})
	v.togglePause()
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("b", 2), xeTestEvent("c", 3)}})
	if got := shownNames(v); !slices.Equal(got, []string{"a"}) {
		t.Errorf("paused: shown %v, want [a]", got)
	}
	if !strings.Contains(v.grid.Status(), "Paused — 2 new since the pause") {
		t.Errorf("paused status %q", v.grid.Status())
	}
	v.togglePause()
	if got := shownNames(v); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("resumed: shown %v, want [a b c]", got)
	}
}

func TestXEventViewerFilterNarrowsHeldAndArrivingEvents(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("fast", 1, "duration", "10"), xeTestEvent("slow", 2, "duration", "5000")}})
	f, err := xevent.ParseFilter("duration > 1000")
	if err != nil {
		t.Fatal(err)
	}
	v.setFilter(f)
	if got := shownNames(v); !slices.Equal(got, []string{"slow"}) {
		t.Fatalf("filtered: %v", got)
	}
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("fast2", 3, "duration", "20"), xeTestEvent("slow2", 4, "duration", "9000")}})
	if got := shownNames(v); !slices.Equal(got, []string{"slow", "slow2"}) {
		t.Errorf("arriving: %v", got)
	}
	if !strings.Contains(v.grid.Status(), "2 of 4 events match the filter") {
		t.Errorf("status %q", v.grid.Status())
	}
	v.setFilter(nil)
	if len(v.shown) != 4 {
		t.Errorf("cleared filter shows %d, want 4", len(v.shown))
	}
}

// Filter by This Value ANDs onto every branch of the filter in force.
func TestXEventViewerFilterByValue(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.applyBatch(xeBatch{events: []xevent.Event{
		xeTestEvent("a", 1, "db", "x"), xeTestEvent("b", 2, "db", "y"), xeTestEvent("a", 3, "db", "y")}})
	f, _ := xevent.ParseFilter("name = a or name = b")
	v.setFilter(f)
	v.filterByValue(xevent.Column{Kind: xevent.ColField, Name: "db"}, "y")
	if len(v.shown) != 2 || v.shown[0].Seq != 2 || v.shown[1].Seq != 3 {
		t.Errorf("shown %v, want the two db=y events", shownNames(v))
	}
}

// New fields grow the grid's columns; hidden ones stay hidden, are saved under
// the session's name, and a new viewer on the session starts with them hidden.
func TestXEventViewerColumnsGrowAndHiddenOnesPersist(t *testing.T) {
	scratchConfigHome(t)
	v := newTestXEventViewer(t, 100)
	v.app.cfg = &config.Config{}
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("a", 1, "duration", "1")}})
	v.applyBatch(xeBatch{events: []xevent.Event{xeTestEvent("b", 2, "duration", "2", "cpu_time", "3")}})
	if got := v.headers(); !slices.Equal(got, []string{"name", "timestamp", "duration", "cpu_time"}) {
		t.Fatalf("headers %v", got)
	}
	if row := v.grid.Row(1); !slices.Equal(row[2:], []string{"2", "3"}) {
		t.Errorf("row 1 cells %v", row)
	}
	v.toggleColumn("field:duration")
	if got := v.headers(); !slices.Equal(got, []string{"name", "timestamp", "cpu_time"}) {
		t.Errorf("after hiding duration: %v", got)
	}
	waitForSaves(t, v.app)
	saved := config.Load().XEventHiddenColumns["s"]
	if !slices.Equal(saved, []string{"field:duration"}) {
		t.Errorf("saved %v, want [field:duration]", saved)
	}
	w := NewXEventViewer(v.app, nil, "s", "", true)
	if !w.hiddenCols["field:duration"] {
		t.Error("a new viewer on the session did not start with duration hidden")
	}
	other := NewXEventViewer(v.app, nil, "other", "", true)
	if len(other.hiddenCols) != 0 {
		t.Error("another session inherited the hidden columns")
	}
}

func TestXEventViewerKeepsTheLastColumn(t *testing.T) {
	v := newTestXEventViewer(t, 100)
	v.toggleColumn("timestamp")
	v.toggleColumn("name")
	if len(v.columns) != 1 || v.columns[0] != xevent.NameColumn {
		t.Errorf("columns %v, want name alone", v.columns)
	}
}

// A cell shows a bounded, one-line cut of a value; the whole value is what
// Show Value and Export use.
func TestXEventCellTextIsBoundedAndOneLine(t *testing.T) {
	long := strings.Repeat("é", xeMaxCellRunes+50)
	got := xeCellText(long)
	if n := len([]rune(got)); n != xeMaxCellRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("cut to %d runes (suffix %q), want %d plus an ellipsis", n, got[len(got)-3:], xeMaxCellRunes)
	}
	if xeCellText("a\nb\tc") != "a b c" {
		t.Errorf("not flattened: %q", xeCellText("a\nb\tc"))
	}
}

// -- The reader, against a scripted server --------------------------------------------

func xeEventXML(name string, seq int) string {
	return fmt.Sprintf(`<event name="%s" package="sqlserver" timestamp="2026-09-29T10:00:%02d.000Z">`+
		`<data name="duration"><value>%d</value></data>`+
		`<action name="event_sequence" package="package0"><value>%d</value></action></event>`, name, seq, seq*10, seq)
}

// runningZZTrace is zz_trace's by-name read with the session running.
func runningZZTrace() []fakeResponse {
	rs := xeSessionByName("zz_trace")
	rs[0].rows[0] = slices.Clone(rs[0].rows[0])
	rs[0].rows[0][3] = true
	return rs
}

// Watch Live Data on an event_file starts at the file the session is writing
// now, not at the oldest file on disk; the next read goes on from that file's
// cursor across the whole rollover set.
func TestXEventReaderLiveEventFileStartsAtTheCurrentFile(t *testing.T) {
	const current = `C:\xe\zz_trace_0_133.xel`
	sc, inst := newFakeConn(t, slices.Concat(runningZZTrace(), []fakeResponse{
		{match: "RingBufferTarget/@truncated", arg: "zz_trace", cols: 12, rows: [][]driver.Value{
			{time.Now(), int64(0), int64(0), "package0", "event_file", int64(1), int64(0), current, nil, nil, nil, nil},
		}},
		// The wildcard read, placed first: its cursor also names the current
		// file, so the current-file answer would otherwise serve it.
		{match: "fn_xe_file_target_read_file", arg: "zz_trace*.xel", cols: 3, rows: [][]driver.Value{
			{current, int64(8192), xeEventXML("rpc_completed", 3)},
		}},
		{match: "fn_xe_file_target_read_file", arg: current, cols: 3, rows: [][]driver.Value{
			{current, int64(0), xeEventXML("rpc_completed", 1)},
			{current, int64(4096), xeEventXML("sql_batch_completed", 2)},
		}},
	}, xeSessionResponses())...)

	r := &xeReader{sc: sc, session: "zz_trace", live: true}
	b, _, err := r.read(context.Background())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(b.events) != 2 || b.source != gosmo.XETargetEventFile {
		t.Fatalf("first read: %d events from %q, want 2 from event_file", len(b.events), b.source)
	}
	args, _ := inst.ReadArgs("fn_xe_file_target_read_file")
	if args[0].Value != current || args[1].Value != nil {
		t.Errorf("first read's pattern %v, cursor %v — want the current file from its start", args[0].Value, args[1].Value)
	}

	b, _, err = r.read(context.Background())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(b.events) != 1 || b.events[0].Seq != 3 {
		t.Fatalf("second read: %+v", b.events)
	}
	inst.mu.Lock()
	var last []driver.NamedValue
	for i, q := range inst.reads {
		if strings.Contains(q, "fn_xe_file_target_read_file") {
			last = inst.readArgs[i]
		}
	}
	inst.mu.Unlock()
	if last[0].Value != "zz_trace*.xel" || last[1].Value != current || last[2].Value != int64(4096) {
		t.Errorf("second read's args %v %v %v — want the wildcard from the first read's last buffer", last[0].Value, last[1].Value, last[2].Value)
	}
}

// A stopped session has nothing arriving to watch.
func TestXEventReaderLiveRefusesAStoppedSession(t *testing.T) {
	sc, _ := newFakeConn(t, append(xeSessionByName("zz_trace"), xeSessionResponses()...)...)
	r := &xeReader{sc: sc, session: "zz_trace", live: true}
	if _, _, err := r.read(context.Background()); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err %v, want not running", err)
	}
}

// A re-read ring_buffer yields only the events not held from the last read,
// and events the target processed but no read saw are counted as missing.
func TestXEventReaderRingBufferDedupesAndCountsTheGap(t *testing.T) {
	ring := func(processed int, seqs ...int) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<RingBufferTarget truncated="0" processingTime="0" totalEventsProcessed="%d" eventCount="%d" droppedCount="0" memoryUsed="1">`, processed, len(seqs))
		for _, s := range seqs {
			b.WriteString(xeEventXML("wait_info", s))
		}
		b.WriteString("</RingBufferTarget>")
		return b.String()
	}
	sc, inst := newFakeConn(t, slices.Concat(runningZZTrace(), []fakeResponse{
		{match: "target_name = N'ring_buffer'", cols: 1, rows: [][]driver.Value{{ring(2, 1, 2)}}},
	}, xeSessionResponses())...)

	r := &xeReader{sc: sc, session: "zz_trace", target: gosmo.XETargetRingBuffer}
	b, _, err := r.read(context.Background())
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(b.events) != 2 || b.missing != 0 {
		t.Fatalf("first read: %d events, %d missing", len(b.events), b.missing)
	}

	// Seq 3 came and went between the reads; 2 is still held.
	inst.mu.Lock()
	for i := range inst.responses {
		if inst.responses[i].match == "target_name = N'ring_buffer'" {
			inst.responses[i].rows = [][]driver.Value{{ring(5, 2, 4, 5)}}
		}
	}
	inst.mu.Unlock()
	b, _, err = r.read(context.Background())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	var seqs []uint64
	for _, e := range b.events {
		seqs = append(seqs, e.Seq)
	}
	if !slices.Equal(seqs, []uint64{4, 5}) || b.missing != 1 {
		t.Errorf("second read: seqs %v, missing %d — want [4 5] and 1", seqs, b.missing)
	}
}

// View Target Data on a target that keeps aggregates says so rather than
// showing an empty grid.
func TestXEventReaderRefusesAnAggregatingTarget(t *testing.T) {
	sc, _ := newFakeConn(t, append(runningZZTrace(), xeSessionResponses()...)...)
	r := &xeReader{sc: sc, session: "zz_trace", target: "histogram"}
	if _, _, err := r.read(context.Background()); err == nil || !strings.Contains(err.Error(), "aggregated") {
		t.Errorf("err %v", err)
	}
}

func TestEventTargetMenuOffersViewTargetDataOnlyWhereItCanRead(t *testing.T) {
	for name, want := range map[string]bool{"event_file": true, "ring_buffer": true, "histogram": false, "pair_matching": false} {
		node := &explorerNode{data: nodeData{Type: NodeEventTarget, Name: name, XESession: "s"}}
		items := eventTargetMenuItems(newTestApp(), nil, node, controls.MenuItem{Label: "New Query"}, controls.MenuItem{Label: "Refresh"})
		if items[0].Label != "View Target Data" || items[0].Enabled() != want {
			t.Errorf("%s: %q enabled=%v, want %v", name, items[0].Label, items[0].Enabled(), want)
		}
	}
}
