package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/xevent"
)

// The XEvent Profiler (Phase D): the sessions it creates, Launch Session's
// create-then-start, the stop question on closing its viewer, and the Phase C
// leftovers folded in — View Target Data newest file first, a live event_file
// finding its file deleted by rollover, the event limit from Options.

func TestXEProfilerSpecIsTheTemplateUnderOurNameWithATarget(t *testing.T) {
	for _, k := range xeProfilerKinds {
		spec := k.spec(false)
		if spec.Name != k.session || !strings.HasPrefix(spec.Name, "gossms_QuickSession") {
			t.Errorf("%s: name %q", k.label, spec.Name)
		}
		if spec.MaxDispatchLatency != 3*time.Second {
			t.Errorf("%s: latency %v", k.label, spec.MaxDispatchLatency)
		}
		if len(spec.Targets) != 1 || spec.Targets[0].Name != gosmo.XETargetEventFile {
			t.Fatalf("%s: targets %+v", k.label, spec.Targets)
		}
		if f, _ := spec.Targets[0].Field("filename"); f != k.session {
			t.Errorf("%s: event_file writes %q", k.label, f)
		}
		for _, e := range spec.Events {
			if !slices.Contains(e.Actions, "package0.event_sequence") {
				t.Errorf("%s: %s collects no event_sequence", k.label, e.Name)
			}
			if !strings.Contains(e.Predicate, "[sqlserver].[client_app_name]<>N'goSSMS - XEvent Profiler'") {
				t.Errorf("%s: %s records the viewer's own reads: %q", k.label, e.Name, e.Predicate)
			}
		}
		if p := spec.Events[slices.IndexFunc(spec.Events, func(e gosmo.SessionEvent) bool { return e.Name == "sql_batch_starting" })].Predicate; !strings.Contains(p, "[is_system]") {
			t.Errorf("%s: the template's own predicate was lost: %q", k.label, p)
		}
		if len(spec.Events) != len(k.template().Events) {
			t.Errorf("%s: %d events, the template has %d", k.label, len(spec.Events), len(k.template().Events))
		}
		if az := k.spec(true); len(az.Targets) != 1 || az.Targets[0].Name != gosmo.XETargetRingBuffer {
			t.Errorf("%s on Azure: targets %+v, want a ring_buffer", k.label, az.Targets)
		}
	}
}

func TestXEventProfilerFolderListsBothTemplates(t *testing.T) {
	kids, _ := loadXEventProfilerChildren(loaderCtx{}, &explorerNode{})
	if got := labelsOfNodes(kids); !slices.Equal(got, []string{"Standard", "TSQL"}) {
		t.Errorf("children %v", got)
	}
	for _, n := range kids {
		if n.data.Type != NodeXEventProfilerSession {
			t.Errorf("%s is a %v", n.label, n.data.Type)
		}
	}
}

// Launch creates the session when it is missing and starts it, then opens the
// viewer marked as the Profiler's; on a session that exists and runs it writes
// nothing.
func TestLaunchSessionCreatesStartsAndOpensTheViewer(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t, fakeResponse{match: "s.event_retention_mode_desc", cols: 12})
	a.launchXEventProfilerNamed(sc, "TSQL")
	waitAndDrain(t, a)
	stmts := inst.Statements()
	if len(stmts) != 2 || !strings.HasPrefix(stmts[0], "CREATE EVENT SESSION [gossms_QuickSessionTSQL] ON SERVER") ||
		!strings.Contains(stmts[0], "ADD TARGET package0.event_file(SET filename=N'gossms_QuickSessionTSQL',max_file_size=(20),max_rollover_files=(4))") ||
		stmts[1] != "ALTER EVENT SESSION [gossms_QuickSessionTSQL] ON SERVER STATE = START" {
		t.Fatalf("statements %q (status %q)", stmts, a.statusText)
	}
	v := openXEventViewerFor(t, a, "gossms_QuickSessionTSQL")
	if !v.profiler || !v.live {
		t.Errorf("viewer profiler=%v live=%v", v.profiler, v.live)
	}

	// Running already: reused as it is.
	a = newTestApp()
	running := xeSessionByName("zz_trace")
	for i := range running {
		running[i].arg = "gossms_QuickSessionStandard"
	}
	running[0].rows[0] = slices.Clone(running[0].rows[0])
	running[0].rows[0][1], running[0].rows[0][3] = "gossms_QuickSessionStandard", true
	sc, inst = newFakeConn(t, running...)
	a.launchXEventProfilerNamed(sc, "Standard")
	waitAndDrain(t, a)
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a running session was written to: %q", stmts)
	}
	openXEventViewerFor(t, a, "gossms_QuickSessionStandard")
}

func openXEventViewerFor(t *testing.T, a *App, session string) *XEventViewer {
	t.Helper()
	i := a.panels.FindIndex(func(p layout.Panel) bool {
		v, ok := p.(*XEventViewer)
		return ok && v.session == session
	})
	if i < 0 {
		t.Fatalf("no viewer on %s", session)
	}
	return a.panels.PanelAt(i).(*XEventViewer)
}

// Closing the Profiler's viewer asks, with No focused, whether to stop the
// session; Enter straight through keeps it running. Another viewer closes
// without asking.
func TestClosingTheProfilersViewerAsksToStopDefaultingToNo(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t)
	v := NewXEventViewer(a, sc, "gossms_QuickSessionStandard", "", true)
	v.profiler, v.conn = true, ownConn(t)
	a.panels.AddPanel(v)
	a.requestClosePanel(a.panels.FindIndex(func(p layout.Panel) bool { return p == v }))
	if !a.confirmDialog.Visible() {
		t.Fatal("no question on closing the Profiler's viewer")
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if a.progressBusy { // a Yes would be stopping the session now
		waitAndDrain(t, a)
	}
	if a.panelHosted(v) || len(inst.Statements()) != 0 {
		t.Errorf("Enter: hosted %v, statements %q — want closed and left running", a.panelHosted(v), inst.Statements())
	}

	v = NewXEventViewer(a, sc, "gossms_QuickSessionStandard", "", true)
	v.profiler, v.conn = true, ownConn(t)
	a.panels.AddPanel(v)
	a.requestClosePanel(a.panels.FindIndex(func(p layout.Panel) bool { return p == v }))
	// Yes has to be chosen: Tab from No wraps round to it.
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	waitAndDrain(t, a)
	if want := "ALTER EVENT SESSION [gossms_QuickSessionStandard] ON SERVER STATE = STOP"; !slices.Equal(inst.Statements(), []string{want}) {
		t.Errorf("Yes: statements %q", inst.Statements())
	}

	w := NewXEventViewer(a, sc, "zz_trace", "", true)
	w.conn = ownConn(t)
	a.panels.AddPanel(w)
	a.requestClosePanel(a.panels.FindIndex(func(p layout.Panel) bool { return p == w }))
	if a.confirmDialog.Visible() || a.panelHosted(w) {
		t.Error("a viewer the Profiler did not open asked before closing")
	}
}

// ownConn stands for a viewer's own connection, which closing the viewer
// closes — never the host's.
func ownConn(t *testing.T) *db.ServerConn {
	sc, _ := newFakeConn(t)
	return sc
}

// fileRead answers an exact-path read of one .xel file with its events.
func fileRead(path string, seqs ...int) fakeResponse {
	var rows [][]driver.Value
	for _, s := range seqs {
		rows = append(rows, []driver.Value{path, int64(s * 1024), xeEventXML("sql_batch_completed", s)})
	}
	return fakeResponse{match: "fn_xe_file_target_read_file", arg: path, cols: 3, rows: rows}
}

// fileArgs returns the arguments of the event_file read of path, nil if none.
func fileArgs(inst *fakeInstance, path string) []driver.NamedValue {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	for i, q := range inst.reads {
		if strings.Contains(q, "fn_xe_file_target_read_file") && inst.readArgs[i][0].Value == path {
			return inst.readArgs[i]
		}
	}
	return nil
}

// View Target Data on a file set reads the newest file first and goes back
// only until the store's capacity is covered, then hands the events over
// oldest first; the older files are counted, never read.
func TestXEventReaderReadsTheNewestFilesFirst(t *testing.T) {
	const f1, f2, f3 = `C:\log\zz_trace_0_100.xel`, `C:\log\zz_trace_0_200.xel`, `C:\log\zz_trace_0_300.xel`
	dmf := func(path string) []driver.Value {
		return []driver.Value{path, path[len(`C:\log\`):], int64(0), int64(1), time.Time{}}
	}
	sc, inst := newFakeConn(t, slices.Concat(xeSessionByName("zz_trace"), []fakeResponse{
		{match: "ErrorLogFileName", cols: 1, rows: [][]driver.Value{{`C:\log\ERRORLOG`}}},
		{match: "sys.dm_os_enumerate_filesystem", cols: 5, rows: [][]driver.Value{dmf(f3), dmf(f1), dmf(f2)}},
		fileRead(f3, 5, 6), fileRead(f2, 3, 4), fileRead(f1, 1, 2),
	}, xeSessionResponses())...)

	r := &xeReader{sc: sc, session: "zz_trace", target: gosmo.XETargetEventFile, capacity: 3}
	var got []uint64
	var skipped int
	if err := r.run(context.Background(), func(b xeBatch) {
		for _, e := range b.events {
			got = append(got, e.Seq)
		}
		skipped += b.skipped
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []uint64{3, 4, 5, 6}) || skipped != 1 {
		t.Errorf("events %v, skipped %d — want [3 4 5 6] oldest first and the oldest file skipped", got, skipped)
	}
	if fileArgs(inst, f1) != nil {
		t.Error("the oldest file was read")
	}
	if fileArgs(inst, f3) == nil {
		t.Error("the newest file was not read by its path")
	}
}

// A listing with one file (or none) reads with the pattern, as before.
func TestXEventReaderFallsBackToThePatternWithoutAFileList(t *testing.T) {
	sc, inst := newFakeConn(t, slices.Concat(xeSessionByName("zz_trace"), []fakeResponse{
		{match: "ErrorLogFileName", cols: 1, rows: [][]driver.Value{{`C:\log\ERRORLOG`}}},
		{match: "sys.dm_os_enumerate_filesystem", cols: 5},
		fileRead("zz_trace*.xel", 1, 2),
	}, xeSessionResponses())...)
	r := &xeReader{sc: sc, session: "zz_trace", target: gosmo.XETargetEventFile, capacity: 3}
	n := 0
	if err := r.run(context.Background(), func(b xeBatch) { n += len(b.events) }); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d events", n)
	}
	if fileArgs(inst, "zz_trace*.xel") == nil {
		t.Error("the pattern was not read")
	}
}

// A live event_file whose cursor file rollover deleted carries on from the
// oldest file left, and says so, rather than stopping.
func TestXEventReaderLiveRepositionsAfterRollover(t *testing.T) {
	const gone = `C:\xe\zz_trace_0_1.xel`
	sc, inst := newFakeConn(t, slices.Concat(runningZZTrace(), []fakeResponse{
		{match: "fn_xe_file_target_read_file", arg: gone, err: mssql.Error{Number: 25722, Class: 16,
			Message: `The offset 4096 is invalid for log file "` + gone + `".`}},
		fileRead("zz_trace*.xel", 7),
	}, xeSessionResponses())...)
	r := &xeReader{sc: sc, session: "zz_trace", live: true, positioned: true,
		cursor: gosmo.EventFileCursor{File: gone, Offset: 4096}}
	b, more, err := r.read(context.Background())
	if err != nil || !b.rolledOver || !more {
		t.Fatalf("first read: rolledOver %v, more %v, err %v", b.rolledOver, more, err)
	}
	b, _, err = r.read(context.Background())
	if err != nil || len(b.events) != 1 || b.events[0].Seq != 7 {
		t.Fatalf("after repositioning: %+v, %v", b.events, err)
	}
	inst.mu.Lock()
	var last []driver.NamedValue
	for i, q := range inst.reads {
		if strings.Contains(q, "fn_xe_file_target_read_file") {
			last = inst.readArgs[i]
		}
	}
	inst.mu.Unlock()
	if last[0].Value != "zz_trace*.xel" || last[1].Value != nil {
		t.Errorf("repositioned read %v %v — want the wildcard from the start", last[0].Value, last[1].Value)
	}

	v := newTestXEventViewer(t, 10)
	v.applyBatch(xeBatch{rolledOver: true})
	v.applyBatch(xeBatch{skipped: 2, events: []xevent.Event{xeTestEvent("a", 1)}})
	for _, want := range []string{"rollover deleted a file", "2 older files not read"} {
		if !strings.Contains(v.grid.Status(), want) {
			t.Errorf("status %q lacks %q", v.grid.Status(), want)
		}
	}
}

// Watch Live Data on a session with nothing to read offers to add a target,
// and Yes adds it; a login that could not add one is not asked.
func TestWatchingASessionWithNoTargetOffersToAddOne(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t)
	v := NewXEventViewer(a, sc, "zz_bare", "", true)
	a.panels.AddPanel(v)
	v.feed.running = true
	task, _ := a.startTask(context.Background(), "watch")
	_, seq := v.feed.run.Begin(context.Background())
	v.feedEnded(seq, task, errXENoTarget)
	answerConfirm(t, a, false)
	waitAndDrain(t, a)
	if want := "ALTER EVENT SESSION [zz_bare] ON SERVER\nADD TARGET package0.event_file(SET filename=N'zz_bare')"; !slices.Equal(inst.Statements(), []string{want}) {
		t.Errorf("statements %q", inst.Statements())
	}

	a = newTestApp()
	sc, inst = newFakeConn(t, capabilityResponses(true, nil, []string{"ALTER ANY EVENT SESSION", "ALTER ANY EVENT SESSION ADD TARGET"}, nil, nil)...)
	sc.ProbeCapabilities()
	v = NewXEventViewer(a, sc, "zz_bare", "", true)
	a.panels.AddPanel(v)
	task, _ = a.startTask(context.Background(), "watch")
	_, seq = v.feed.run.Begin(context.Background())
	v.feedEnded(seq, task, errXENoTarget)
	if a.confirmDialog.Visible() || len(inst.Statements()) != 0 {
		t.Error("offered to a login that may not add a target")
	}
}

func TestXEventStoreCapacityComesFromOptions(t *testing.T) {
	if config.DefaultXEventStoreCapacity != xevent.DefaultCapacity {
		t.Errorf("config default %d, store default %d", config.DefaultXEventStoreCapacity, xevent.DefaultCapacity)
	}
	a := newTestApp()
	a.cfg.XEventStoreCapacity = 5000
	if got := NewXEventViewer(a, nil, "s", "", true).store.Capacity(); got != 5000 {
		t.Errorf("capacity %d, want the configured 5000", got)
	}
	for in, want := range map[int]int{0: 100_000, 999: 100_000, 1000: 1000, 10_000_001: 100_000} {
		if got := config.ClampXEventStoreCapacity(in); got != want {
			t.Errorf("Clamp(%d) = %d, want %d", in, got, want)
		}
	}
}

// Launch creates and starts, so it needs both rights: 2022's CREATE ANY EVENT
// SESSION alone created the session and was refused the start (live, major
// 17), leaving it stopped behind.
func TestLaunchSessionNeedsCreateAndStart(t *testing.T) {
	for _, c := range []struct {
		name            string
		granted, denied []string
		want            bool
	}{
		{"wide name", []string{"ALTER ANY EVENT SESSION"}, nil, true},
		{"only VIEW SERVER STATE", nil, []string{"ALTER ANY EVENT SESSION", "CREATE ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE"}, false},
		{"2022, create only", []string{"CREATE ANY EVENT SESSION"}, []string{"ALTER ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE"}, false},
		{"2022, create and enable", []string{"CREATE ANY EVENT SESSION", "ALTER ANY EVENT SESSION ENABLE"}, []string{"ALTER ANY EVENT SESSION"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t, capabilityResponses(true, c.granted, c.denied, nil, nil)...)
			sc.ProbeCapabilities()
			node := &explorerNode{data: nodeData{Type: NodeXEventProfilerSession, Name: "Standard", conn: sc}}
			items := xeventProfilerMenuItems(a, sc, node, controls.MenuItem{}, controls.MenuItem{})
			if got := items[0].Enabled(); got != c.want {
				t.Errorf("Launch Session enabled = %v, want %v", got, c.want)
			}
			if !c.want {
				a.launchXEventProfilerNamed(sc, "Standard")
				if len(inst.Statements()) != 0 || a.statusText != xeProfilerNeedsRight {
					t.Errorf("refused launch: statements %q, status %q", inst.Statements(), a.statusText)
				}
			}
		})
	}
}
