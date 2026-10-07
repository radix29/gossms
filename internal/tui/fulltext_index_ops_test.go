package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// The table's Full-Text index cascade (W18): what each action refuses on the
// index's state, the statement it sends, and the Background Tasks entry that
// follows a population. Statement text is gosmo's to test; the refusals are
// the states the server answers with a warning and no error (probed on 17).

// ftState is the index state a test's read answers with.
type ftState struct {
	enabled    bool
	tracking   string // AUTO, MANUAL, OFF
	crawlStart time.Time
	completed  bool
	status     int64
	items      int64
	pending    int64
}

var ftCrawl0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

// ftIdle is an enabled, idle index under MANUAL tracking with nothing pending.
func ftIdle() ftState {
	return ftState{enabled: true, tracking: "MANUAL", crawlStart: ftCrawl0, completed: true, items: 40}
}

// row is the index read's 21 columns for dbo.Notes on main_cat.
func (s ftState) row() []driver.Value {
	var end any = s.crawlStart.Add(time.Minute)
	if !s.completed {
		end = nil
	}
	return []driver.Value{int64(101), "dbo", "Notes", "PK_Notes", "main_cat", "", s.enabled,
		s.tracking, int64(0), "", "", int64(1),
		"FULL_CRAWL", s.completed, s.crawlStart, end,
		s.items, int64(0), int64(0), s.pending, s.status}
}

// ftOpConn answers findFullTextIndex for appdb's dbo.Notes with s.
func ftOpConn(t *testing.T, s ftState) (*fakeInstance, *explorerNode) {
	t.Helper()
	sc, inst := newFakeConn(t, dbByNameResp("appdb", 5),
		fakeResponse{match: "FROM   sys.tables t", db: "appdb", cols: 12, rows: [][]driver.Value{
			{int64(101), "dbo", "Notes", time.Time{}, time.Time{}, false, false, false, false, false, false, false},
		}},
		fakeResponse{match: ftIndexesRead, db: "appdb", cols: 21, rows: [][]driver.Value{s.row()}},
		fakeResponse{match: ftColumnsRead, db: "appdb", cols: 6})
	return inst, opTestNode(sc, NodeTable, "dbo", "Notes", "")
}

// setFTState changes what the index read answers from now on. The script is
// replaced, not edited: a read in flight holds a pointer into the old one.
func setFTState(inst *fakeInstance, s ftState) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	responses := slices.Clone(inst.responses)
	for k := range responses {
		if responses[k].match == ftIndexesRead {
			responses[k].rows = [][]driver.Value{s.row()}
		}
	}
	inst.responses = responses
}

func ftTestApp() *App {
	a := newTestApp()
	a.alertDialog = dialogs.NewAlertDialog(nil)
	return a
}

// Each refusal is a state the server would accept and ignore (STOP under
// tracking, a START under AUTO or while one runs) or refuse with its own
// number (7660 on a disabled index, 7664 for UPDATE with tracking off).
// Nothing reaches the server, and the alert says why.
func TestFullTextIndexOpsRefuseWhatTheServerWouldIgnore(t *testing.T) {
	running := ftIdle()
	running.status, running.completed = int64(gosmo.FullTextTableFullPopulation), false
	disabled := ftIdle()
	disabled.enabled = false
	auto := ftIdle()
	auto.tracking = "AUTO"
	runningUntracked := running
	runningUntracked.tracking = "OFF"
	off := ftIdle()
	off.tracking = "OFF"

	for _, c := range []struct {
		name  string
		op    fullTextIndexOp
		state ftState
		want  string // in the alert; "" means the action goes ahead
	}{
		{"stop under MANUAL", fullTextStopOp(), running, "ignores Stop Population"},
		{"stop under AUTO", fullTextStopOp(), func() ftState { s := running; s.tracking = "AUTO"; return s }(), "Automatic"},
		{"stop when idle", fullTextStopOp(), off, "No population is running"},
		{"stop untracked and running", fullTextStopOp(), runningUntracked, ""},
		{"full while one runs", fullTextStartOp(gosmo.FullTextPopulationFull), running, "already running"},
		{"incremental on a disabled index", fullTextStartOp(gosmo.FullTextPopulationIncremental), disabled, "is disabled"},
		{"full when idle", fullTextStartOp(gosmo.FullTextPopulationFull), ftIdle(), ""},
		{"full under AUTO", fullTextStartOp(gosmo.FullTextPopulationFull), auto, "ignores Start Full Population"},
		{"incremental under AUTO", fullTextStartOp(gosmo.FullTextPopulationIncremental), auto, "ignores Start Incremental Population"},
		{"full untracked", fullTextStartOp(gosmo.FullTextPopulationFull), off, ""},
		{"apply under AUTO", fullTextApplyTrackedChangesOp(), auto, "Automatic"},
		{"apply untracked", fullTextApplyTrackedChangesOp(), off, "is Off"},
		{"apply with nothing pending", fullTextApplyTrackedChangesOp(), ftIdle(), "No tracked changes"},
		{"apply with changes pending", fullTextApplyTrackedChangesOp(), func() ftState { s := ftIdle(); s.pending = 3; return s }(), ""},
		{"enable an enabled index", fullTextEnableOp(), ftIdle(), "already enabled"},
		{"disable a disabled index", fullTextDisableOp(), disabled, "already disabled"},
		{"track MANUAL again", fullTextTrackChangesOp(gosmo.FullTextChangeTrackingManual), ftIdle(), "already Manual"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := ftTestApp()
			inst, node := ftOpConn(t, c.state)
			a.runFullTextIndexOp(node.data.conn, node, c.op)
			waitAndDrain(t, a)
			if c.want == "" {
				if a.alertDialog.Visible() {
					t.Fatalf("refused: %q", a.alertDialog.Message())
				}
				return
			}
			if !a.alertDialog.Visible() || !strings.Contains(a.alertDialog.Message(), c.want) {
				t.Errorf("alert = %v %q, want one containing %q", a.alertDialog.Visible(), a.alertDialog.Message(), c.want)
			}
			if s := inst.StatementsIn("appdb"); len(s) != 0 {
				t.Errorf("a refused action wrote: %q", s)
			}
		})
	}
}

// A table without a full-text index says so instead of sending a statement
// the server answers with Msg 7658.
func TestFullTextIndexOpOnATableWithoutOne(t *testing.T) {
	a := ftTestApp()
	sc, inst := newFakeConn(t, dbByNameResp("appdb", 5),
		fakeResponse{match: "FROM   sys.tables t", db: "appdb", cols: 12, rows: [][]driver.Value{
			{int64(103), "dbo", "Plain", time.Time{}, time.Time{}, false, false, false, false, false, false, false},
		}},
		fakeResponse{match: ftIndexesRead, db: "appdb", cols: 21})
	node := opTestNode(sc, NodeTable, "dbo", "Plain", "")
	a.runFullTextIndexOp(sc, node, fullTextStartOp(gosmo.FullTextPopulationFull))
	waitAndDrain(t, a)
	if !strings.Contains(a.alertDialog.Message(), "[dbo].[Plain] has no full-text index") {
		t.Errorf("alert = %q", a.alertDialog.Message())
	}
	if s := inst.StatementsIn("appdb"); len(s) != 0 {
		t.Errorf("wrote %q", s)
	}
}

// Disable and Delete ask first; a No sends nothing, a Yes sends the one
// statement.
func TestFullTextIndexDisableAndDeleteAskFirst(t *testing.T) {
	for _, c := range []struct {
		op   fullTextIndexOp
		stmt string
	}{
		{fullTextDisableOp(), "ALTER FULLTEXT INDEX ON [dbo].[Notes] DISABLE"},
		{fullTextDeleteOp(), "DROP FULLTEXT INDEX ON [dbo].[Notes]"},
	} {
		t.Run(c.op.title, func(t *testing.T) {
			for _, yes := range []bool{false, true} {
				a := ftTestApp()
				inst, node := ftOpConn(t, ftIdle())
				a.runFullTextIndexOp(node.data.conn, node, c.op)
				waitAndDrain(t, a)
				if !a.confirmDialog.Visible() {
					t.Fatal("ran without asking")
				}
				if !yes {
					a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
					if s := inst.StatementsIn("appdb"); len(s) != 0 {
						t.Errorf("No still wrote %q", s)
					}
					continue
				}
				answerConfirm(t, a, false)
				waitAndDrain(t, a)
				assertOneStatementIn(t, inst, "appdb", c.stmt)
			}
		})
	}
}

// Turning tracking off asks only when it would discard tracked changes.
func TestFullTextTrackingOffAsksOnlyWithChangesPending(t *testing.T) {
	for _, pending := range []int64{0, 4} {
		a := ftTestApp()
		s := ftIdle()
		s.pending = pending
		inst, node := ftOpConn(t, s)
		a.runFullTextIndexOp(node.data.conn, node, fullTextTrackChangesOp(gosmo.FullTextChangeTrackingOff))
		waitAndDrain(t, a)
		if got := a.confirmDialog.Visible(); got != (pending > 0) {
			t.Fatalf("pending %d: asked = %v", pending, got)
		}
		if pending > 0 {
			if msg := a.confirmDialog.Message(); !strings.Contains(msg, "4 changes tracked") {
				t.Errorf("prompt = %q, want the count", msg)
			}
			answerConfirm(t, a, false)
			waitAndDrain(t, a)
		}
		assertOneStatementIn(t, inst, "appdb", "ALTER FULLTEXT INDEX ON [dbo].[Notes] SET CHANGE_TRACKING = OFF")
	}
}

// Start Full Population sends START FULL and lists a task that follows it:
// running while the read shows the crawl it started in progress, finished
// once that crawl completes, with the counters on the status line.
func TestFullTextStartFollowsThePopulationToItsEnd(t *testing.T) {
	defer func(d time.Duration) { fullTextPollInterval = d }(fullTextPollInterval)
	fullTextPollInterval = time.Millisecond

	a := ftTestApp()
	inst, node := ftOpConn(t, ftIdle())
	a.runFullTextIndexOp(node.data.conn, node, fullTextStartOp(gosmo.FullTextPopulationFull))
	waitAndDrain(t, a)

	// The crawl the START began, before the progress dialog's done runs and
	// the follow's first read.
	crawl := ftIdle()
	crawl.crawlStart, crawl.completed = ftCrawl0.Add(time.Hour), false
	crawl.status, crawl.items = int64(gosmo.FullTextTableFullPopulation), 12
	setFTState(inst, crawl)
	drainUntil(t, a, func() bool { return len(a.tasks) == 1 && a.tasks[0].Message != "" }, "the follow's first progress")
	assertOneStatementIn(t, inst, "appdb", "ALTER FULLTEXT INDEX ON [dbo].[Notes] START FULL POPULATION")
	task := a.tasks[0]
	if task.Label != "Full population — [dbo].[Notes]" {
		t.Errorf("label = %q", task.Label)
	}
	if task.Message != "12 rows indexed" {
		t.Errorf("progress = %q", task.Message)
	}

	crawl.completed, crawl.status, crawl.items = true, 0, 40
	setFTState(inst, crawl)
	drainUntil(t, a, func() bool { return task.Done }, "the follow to finish")
	if task.Err != nil {
		t.Fatalf("task failed: %v", task.Err)
	}
	if want := "Full population — [dbo].[Notes] finished — 40 rows indexed"; a.statusText != want {
		t.Errorf("status = %q, want %q", a.statusText, want)
	}
}

// After an ENABLE nothing populates under OFF tracking: no task is listed.
func TestFullTextEnableListsNoTaskWhenNothingPopulates(t *testing.T) {
	a := ftTestApp()
	s := ftIdle()
	s.enabled, s.tracking = false, "OFF"
	inst, node := ftOpConn(t, s)
	a.runFullTextIndexOp(node.data.conn, node, fullTextEnableOp())
	waitAndDrain(t, a)
	assertOneStatementIn(t, inst, "appdb", "ALTER FULLTEXT INDEX ON [dbo].[Notes] ENABLE")
	// The check for a population posts nothing when it finds none; give it
	// the time a post would take.
	time.Sleep(50 * time.Millisecond)
	a.drainPending()
	if len(a.tasks) != 0 {
		t.Errorf("tasks = %d, want none", len(a.tasks))
	}
}

func TestFullTextPopulationPoll(t *testing.T) {
	idx := func(start time.Time, completed bool, status gosmo.FullTextTablePopulateStatus) *gosmo.FullTextIndex {
		return &gosmo.FullTextIndex{CrawlStart: start, CrawlCompleted: completed, PopulateStatus: status,
			ItemCount: 7, PendingChanges: 2, FailCount: 1}
	}
	later := ftCrawl0.Add(time.Second)

	idle := 0
	if done, msg := fullTextPopulationPoll(ftCrawl0, idx(later, true, gosmo.FullTextTableIdle), &idle); !done ||
		msg != "finished — 7 rows indexed, 2 changes pending, 1 failed" {
		t.Errorf("a completed new crawl: done %v, %q", done, msg)
	}
	if done, msg := fullTextPopulationPoll(ftCrawl0, idx(later, false, gosmo.FullTextTablePropagatingChanges), &idle); done ||
		msg != "7 rows indexed, 2 changes pending, 1 failed" {
		t.Errorf("a running new crawl: done %v, %q", done, msg)
	}
	// A paused or throttled one says so: the label cannot.
	if _, msg := fullTextPopulationPoll(ftCrawl0, idx(later, false, gosmo.FullTextTableThrottledOrPaused), &idle); !strings.HasPrefix(msg, gosmo.FullTextTableThrottledOrPaused.String()+" — ") {
		t.Errorf("a paused crawl: %q", msg)
	}
	// The old crawl still running is not "nothing started".
	for range fullTextNoPopulationPolls + 1 {
		if done, _ := fullTextPopulationPoll(ftCrawl0, idx(ftCrawl0, false, gosmo.FullTextTableFullPopulation), &idle); done {
			t.Fatal("a running crawl that predates the action ended the follow")
		}
	}
	// Only idle reads with no new crawl count toward giving up.
	for k := 1; k <= fullTextNoPopulationPolls; k++ {
		done, msg := fullTextPopulationPoll(ftCrawl0, idx(ftCrawl0, true, gosmo.FullTextTableIdle), &idle)
		if done != (k == fullTextNoPopulationPolls) {
			t.Fatalf("idle read %d: done = %v", k, done)
		}
		if done && msg != "— no population ran" {
			t.Errorf("gave up with %q", msg)
		}
	}
}

// The cascade in SSMS's order, every write gated on ALTER on the table — a
// login granted it on one table keeps them there and loses them on its
// neighbour — and Properties never gated.
func TestTableFullTextCascadeIsGatedOnTheTable(t *testing.T) {
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)
	cascade := func(name string) []controls.MenuItem {
		node := &explorerNode{data: nodeData{Type: NodeTable, DBName: "appdb", Schema: "Sales", Name: name, conn: sc}}
		return tableFullTextMenu(nil, sc, node).Sub
	}
	var labels []string
	for _, it := range cascade("Orders") {
		if !it.Divider {
			labels = append(labels, it.Label)
		}
	}
	want := []string{"Define Full-Text Index...", "Enable Full-Text Index", "Disable Full-Text Index", "Delete Full-Text Index...",
		"Start Full Population", "Start Incremental Population", "Stop Population",
		"Track Changes", "Apply Tracked Changes", "Properties..."}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("cascade = %q\nwant      %q", labels, want)
	}
	for name, allowed := range map[string]bool{"Orders": true, "Customers": false} {
		for _, it := range cascade(name) {
			if it.Divider {
				continue
			}
			got := it.Enabled == nil || it.Enabled()
			if wantOn := allowed || it.Label == "Properties..."; got != wantOn {
				t.Errorf("Sales.%s: %s enabled = %v, want %v", name, it.Label, got, wantOn)
			}
		}
	}
}
