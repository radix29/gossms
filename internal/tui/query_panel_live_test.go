package tui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// fakeLiveSource scripts the poller's two reads: profiles[i] answers the i'th
// QueryProfiles call (the last one repeats), and every InFlightPlan call
// answers for the statement plans names by key, or plansErr.
type fakeLiveSource struct {
	mu          sync.Mutex
	profiles    []fakeProfileAnswer
	calls       int
	planReads   int
	plans       map[string]*gosmo.InFlightPlan
	plansErr    error
	afterLast   func() // runs once the last scripted profile answer has been given
	afterCalled bool
}

type fakeProfileAnswer struct {
	rows []gosmo.QueryProfile
	err  error
}

func (f *fakeLiveSource) QueryProfiles(ctx context.Context, _ int) ([]gosmo.QueryProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := min(f.calls, len(f.profiles)-1)
	f.calls++
	if f.calls >= len(f.profiles) && f.afterLast != nil && !f.afterCalled {
		f.afterCalled = true
		defer f.afterLast()
	}
	a := f.profiles[i]
	return a.rows, a.err
}

func (f *fakeLiveSource) InFlightPlan(ctx context.Context, _ int) (*gosmo.InFlightPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planReads++
	if f.plansErr != nil {
		return nil, f.plansErr
	}
	// The statement the session is running is the one the latest profile read
	// named — as on the server.
	i := min(f.calls, len(f.profiles)) - 1
	if i < 0 || len(f.profiles[i].rows) == 0 {
		return nil, gosmo.ErrNotFound
	}
	if p, ok := f.plans[string(f.profiles[i].rows[0].PlanHandle)]; ok {
		return p, nil
	}
	return nil, gosmo.ErrNotFound
}

// liveRow is one profile row of statement handle.
func liveRow(handle string, node, thread int, rows, est int64) gosmo.QueryProfile {
	return gosmo.QueryProfile{
		RequestID: 0, PlanHandle: []byte(handle), StatementStart: 0, StatementEnd: -1,
		NodeID: node, ThreadID: thread, RowCount: rows, EstimateRowCount: est, OpenTime: 1,
	}
}

func liveInFlight(handle string) *gosmo.InFlightPlan {
	return &gosmo.InFlightPlan{PlanHandle: []byte(handle), StatementStart: 0, StatementEnd: -1, XML: testPlanXML}
}

var fastLiveTiming = liveTiming{first: time.Millisecond, every: time.Millisecond, maxBackoff: 4 * time.Millisecond}

// runPoller runs pollLiveStats over src until it returns or src's script has
// been played through (src.afterLast closes done), collecting every report.
func runPoller(t *testing.T, src *fakeLiveSource) []liveUpdate {
	t.Helper()
	done := make(chan struct{})
	if src.afterLast == nil {
		src.afterLast = func() { close(done) }
	}
	var got []liveUpdate
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		pollLiveStats(t.Context(), src, 57, done, fastLiveTiming, nil, func(u liveUpdate) { got = append(got, u) })
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLiveStats did not return")
	}
	return got
}

// The in-flight plan is tens of kilobytes and a plan serialisation on the
// server: the poller reads it for the first statement, keeps it while the
// counters name the same statement, and reads it again only when the batch
// moves on.
func TestPollLiveStatsRereadsThePlanOnlyWhenTheStatementChanges(t *testing.T) {
	src := &fakeLiveSource{
		profiles: []fakeProfileAnswer{
			{rows: []gosmo.QueryProfile{liveRow("A", 0, 0, 10, 100)}},
			{rows: []gosmo.QueryProfile{liveRow("A", 0, 0, 50, 100)}},
			{rows: []gosmo.QueryProfile{liveRow("B", 0, 0, 1, 7)}},
			{rows: []gosmo.QueryProfile{liveRow("B", 0, 0, 7, 7)}},
		},
		plans: map[string]*gosmo.InFlightPlan{"A": liveInFlight("A"), "B": liveInFlight("B")},
	}
	got := runPoller(t, src)
	if len(got) != 4 {
		t.Fatalf("got %d reports, want 4: %+v", len(got), got)
	}
	if src.planReads != 2 {
		t.Errorf("InFlightPlan read %d times, want 2 (once per statement)", src.planReads)
	}
	if got[0].plan == nil || got[0].plan != got[1].plan {
		t.Error("the same statement's two reports carry different plans — the plan was re-read or rebuilt")
	}
	if got[2].plan == got[1].plan {
		t.Error("the next statement's report still carries the first statement's plan")
	}
	if c := got[1].counters[0]; c.Rows != 50 || c.EstRows != 100 {
		t.Errorf("second report's node 0 = %d of %d, want 50 of 100", c.Rows, c.EstRows)
	}
}

// Threads of one operator merge into one figure, and rows of a statement the
// in-flight plan is not for (another MARS request) stay out of it.
func TestPollLiveStatsMergesThreadsOfTheShownStatementOnly(t *testing.T) {
	other := liveRow("A", 0, 1, 1000, 1000)
	other.RequestID = 1
	src := &fakeLiveSource{
		profiles: []fakeProfileAnswer{{rows: []gosmo.QueryProfile{
			liveRow("A", 0, 1, 30, 50), liveRow("A", 0, 2, 20, 50), other,
		}}},
		plans: map[string]*gosmo.InFlightPlan{"A": liveInFlight("A")},
	}
	got := runPoller(t, src)
	if len(got) != 1 {
		t.Fatalf("got %d reports, want 1", len(got))
	}
	if c := got[0].counters[0]; c.Rows != 50 || c.EstRows != 100 || c.Threads != 2 {
		t.Errorf("node 0 = %d of %d on %d threads, want 50 of 100 on 2", c.Rows, c.EstRows, c.Threads)
	}
}

// A login without VIEW SERVER STATE: one note, then no more reads.
func TestPollLiveStatsStopsWithANoteWhenRefused(t *testing.T) {
	refused := mssql.Error{Number: 300, Class: 14,
		Message: "VIEW SERVER STATE permission was denied on object 'server', database 'master'."}
	src := &fakeLiveSource{profiles: []fakeProfileAnswer{{err: refused}}}
	src.afterLast = func() {} // nothing closes done: the poller must stop by itself
	got := runPoller(t, src)
	if len(got) != 1 || got[0].note != liveRefusedNote {
		t.Fatalf("reports = %+v, want the one refusal note", got)
	}
	if src.calls != 1 {
		t.Errorf("QueryProfiles called %d times, want 1 — polling went on after the refusal", src.calls)
	}

	// The same from the plan read.
	src = &fakeLiveSource{
		profiles: []fakeProfileAnswer{{rows: []gosmo.QueryProfile{liveRow("A", 0, 0, 1, 1)}}},
		plansErr: refused,
	}
	src.afterLast = func() {}
	if got := runPoller(t, src); len(got) != 1 || got[0].note != liveRefusedNote {
		t.Fatalf("plan refused: reports = %+v, want the one refusal note", got)
	}
}

// Any other failure is retried — the batch is still running — and nothing is
// reported until a read succeeds; an idle session (no rows) reports idle.
func TestPollLiveStatsRetriesOtherFailures(t *testing.T) {
	src := &fakeLiveSource{
		profiles: []fakeProfileAnswer{
			{err: errors.New("i/o timeout")},
			{rows: nil},
			{rows: []gosmo.QueryProfile{liveRow("A", 0, 0, 1, 2)}},
		},
		plans: map[string]*gosmo.InFlightPlan{"A": liveInFlight("A")},
	}
	got := runPoller(t, src)
	if len(got) != 2 || !got[0].idle || got[1].note != "" || got[1].plan == nil {
		t.Fatalf("reports = %+v, want idle, then one live update", got)
	}
}

// A plan read answering for a different statement than the counters (the
// batch moved on between the two reads) is not paired with them.
func TestPollLiveStatsDropsAPlanForAnotherStatement(t *testing.T) {
	src := &fakeLiveSource{
		profiles: []fakeProfileAnswer{{rows: []gosmo.QueryProfile{liveRow("A", 0, 0, 1, 2)}}},
		plans:    map[string]*gosmo.InFlightPlan{"A": liveInFlight("B")},
	}
	if got := runPoller(t, src); len(got) != 0 {
		t.Fatalf("reports = %+v, want none", got)
	}
}

// The live tab is labelled for what it shows, and the actual plan arriving
// relabels it; someone watching it lands on the Execution Plan tab.
func TestLiveTabBecomesTheExecutionPlanTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.planView = qp.newPlanView()
	qp.planView.SetLive(nil, nil)

	if tabs := qp.resultTabs(); len(tabs) != 2 || tabs[0] != "Live Query Statistics" || tabs[1] != "Messages" {
		t.Fatalf("running: resultTabs() = %v, want [Live Query Statistics Messages]", tabs)
	}
	if !qp.liveTabActive() {
		t.Fatal("liveTabActive() = false on a fresh live run")
	}

	res := newTestResult(2, false)
	res.PlanXML = []string{testPlanXML}
	qp.setRunResult(res, false)

	want := []string{"Results 1", "Results 2", "Execution Plan", "Messages"}
	tabs := qp.resultTabs()
	if len(tabs) != len(want) {
		t.Fatalf("finished: resultTabs() = %v, want %v", tabs, want)
	}
	for i := range want {
		if tabs[i] != want[i] {
			t.Errorf("finished: resultTabs()[%d] = %q, want %q", i, tabs[i], want[i])
		}
	}
	if !qp.planTabActive() {
		t.Errorf("active tab = %d, want the Execution Plan tab (2)", qp.activeTab)
	}
}

// Off the live tab (on Messages), the finished run picks its tab as any run.
func TestLiveRunFinishedOffTheLiveTabKeepsTheUsualTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.planView = qp.newPlanView()
	qp.planView.SetLive(nil, nil)
	qp.activeTab = 1 // Messages

	res := newTestResult(1, false)
	res.PlanXML = []string{testPlanXML}
	qp.setRunResult(res, false)
	if qp.activeTab != 0 {
		t.Errorf("active tab = %d, want 0 (Results)", qp.activeTab)
	}
}

// A poll queued behind the run's end must not put the finished plan back into
// live mode.
func TestLiveUpdateAfterTheRunEndsIsDropped(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	_, token := qp.liveRun.Begin(context.Background())
	qp.planView = qp.newPlanView()
	qp.planView.SetLive(nil, nil)

	res := newTestResult(1, false)
	res.PlanXML = []string{testPlanXML}
	qp.setRunResult(res, false)
	plan := qp.planView.Plan()

	qp.applyLiveUpdate(token, liveUpdate{plan: plan})
	if qp.planView.Live() {
		t.Error("a late poll put the actual plan back into live mode")
	}
}

// Live Query Statistics rides on the actual plan: turning it on turns that on,
// and turning the actual plan off turns live off.
func TestLiveQueryStatisticsToggleKeepsActualPlanInStep(t *testing.T) {
	a := &App{cfg: newTestApp().cfg}
	a.buildUI()
	a.screen = &fakeSizedScreen{w: 120, h: 40} // the toggles relayout

	a.toggleLiveQueryStatistics()
	if !a.liveStatsEnabled || !a.actualPlanEnabled {
		t.Fatalf("after turning live on: live=%v actual=%v, want both on", a.liveStatsEnabled, a.actualPlanEnabled)
	}
	a.toggleActualExecutionPlan()
	if a.liveStatsEnabled || a.actualPlanEnabled {
		t.Fatalf("after turning actual off: live=%v actual=%v, want both off", a.liveStatsEnabled, a.actualPlanEnabled)
	}
	a.toggleActualExecutionPlan()
	if a.liveStatsEnabled || !a.actualPlanEnabled {
		t.Fatalf("after turning actual on: live=%v actual=%v, want live still off", a.liveStatsEnabled, a.actualPlanEnabled)
	}
	off, on := liveStatsToggleIcon(false), liveStatsToggleIcon(true)
	if off == on || len(off) != len(on) {
		t.Errorf("liveStatsToggleIcon off=%q on=%q, want distinct text of equal width", off, on)
	}
}

// runWatch runs pollLiveStats as a LivePlanPanel does, under watch, until it
// returns or stop closes, collecting every report.
func runWatch(t *testing.T, src *fakeLiveSource, tm liveTiming, watch *liveWatch, stop <-chan struct{}) []liveUpdate {
	t.Helper()
	var got []liveUpdate
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		pollLiveStats(t.Context(), src, 57, stop, tm, watch, func(u liveUpdate) { got = append(got, u) })
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLiveStats did not return")
	}
	return got
}

// A watch on another session has no end of its own, so an idle session is
// polled ever more slowly (up to maxBackoff); a query panel's own run keeps
// its one-a-tick pace, since idle there is only between statements.
func TestPollLiveStatsWatchBacksOffWhileIdle(t *testing.T) {
	tm := liveTiming{first: time.Millisecond, every: time.Millisecond, maxBackoff: time.Hour}
	count := func(watch *liveWatch) int {
		src := &fakeLiveSource{profiles: []fakeProfileAnswer{{}}}
		stop := make(chan struct{})
		time.AfterFunc(150*time.Millisecond, func() { close(stop) })
		runWatch(t, src, tm, watch, stop)
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.calls
	}
	if n := count(&liveWatch{}); n > 10 {
		t.Errorf("a watched idle session was read %d times in 150ms, want the wait doubling", n)
	}
	if n := count(nil); n < 20 {
		t.Errorf("a query panel's own idle run was read %d times in 150ms, want its steady pace", n)
	}
}

// A background tab is polled at maxBackoff, and wake (the panel drawn again)
// reads at once.
func TestPollLiveStatsWatchSlowsInTheBackgroundAndWakes(t *testing.T) {
	tm := liveTiming{first: time.Millisecond, every: time.Millisecond, maxBackoff: time.Hour}
	src := &fakeLiveSource{profiles: []fakeProfileAnswer{{}}}
	wake := make(chan struct{}, 1)
	stop := make(chan struct{})
	watch := &liveWatch{background: func() bool { return true }, wake: wake}
	go func() {
		time.Sleep(50 * time.Millisecond)
		src.mu.Lock()
		n := src.calls
		src.mu.Unlock()
		if n != 0 {
			t.Errorf("a background watch read %d times before its first maxBackoff wait", n)
		}
		wake <- struct{}{}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			src.mu.Lock()
			n = src.calls
			src.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if n == 0 {
			t.Error("wake did not cut the background wait short")
		}
		close(stop)
	}()
	runWatch(t, src, tm, watch, stop)
}

// A session id is reused once its session ends: a row from a session that
// logged in at another time than the pinned one is another login's query,
// and ends the watch rather than showing it under this session's title.
func TestPollLiveStatsWatchStopsWhenTheSessionIDIsReused(t *testing.T) {
	pin := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	ours, theirs := liveRow("A", 0, 0, 10, 100), liveRow("B", 0, 0, 1, 7)
	ours.SessionLoginTime, theirs.SessionLoginTime = pin, pin.Add(time.Hour)
	src := &fakeLiveSource{
		profiles: []fakeProfileAnswer{
			{rows: []gosmo.QueryProfile{ours}},
			{},
			{rows: []gosmo.QueryProfile{theirs}},
		},
		plans: map[string]*gosmo.InFlightPlan{"A": liveInFlight("A"), "B": liveInFlight("B")},
	}
	got := runWatch(t, src, fastLiveTiming, &liveWatch{pin: pin}, nil)
	if len(got) != 3 || got[0].plan == nil || !got[1].idle {
		t.Fatalf("reports = %+v, want a reading, idle, then the end", got)
	}
	if last := got[2]; !last.ended || last.note != liveSessionEndedNote(57) || last.plan != nil {
		t.Errorf("the reused id's query was reported as %+v, want the ended note", last)
	}
}

// A LivePlanPanel without a server says so instead of dereferencing it.
func TestLivePlanPanelStartWithoutAServer(t *testing.T) {
	a := newTestApp()
	lp := newLivePlanPanel(a, &db.ServerConn{}, 57)
	lp.start()
	if lp.state != "not available" || !lp.run.Idle() {
		t.Errorf("state %q, poller idle %v; want not available and no poller", lp.state, lp.run.Idle())
	}
}
