package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
)

// query_panel_live.go is Live Query Statistics: with the toggle on, Execute opens
// a "Live Query Statistics" tab showing the running statement's plan while a
// poller fills in each operator's counters, and the tab becomes the Execution Plan
// tab (the actual plan) when the run ends.
//
// The run itself is an ordinary actual-plan run (SET STATISTICS XML ON), which is
// what makes the session show up in sys.dm_exec_query_profiles on every supported
// version. The poller reads that DMV, and the in-flight showplan when the
// statement changes, on the panel connection's pool, never on the panel's own
// query.Session, which is busy running the batch.

// liveSource is what the poller reads: *gosmo.Server, or a test fake.
type liveSource interface {
	QueryProfiles(ctx context.Context, sessionID int) ([]gosmo.QueryProfile, error)
	InFlightPlan(ctx context.Context, sessionID int) (*gosmo.InFlightPlan, error)
}

// liveTiming paces the poller: the first read comes soon after the batch is sent,
// so a query of a second or two still shows a live picture, then one a second as
// SSMS does. A failed read doubles the wait up to maxBackoff, so a struggling
// server is not hammered by the panel watching it.
type liveTiming struct {
	first, every, maxBackoff time.Duration
}

var livePollTiming = liveTiming{first: 250 * time.Millisecond, every: time.Second, maxBackoff: 8 * time.Second}

// liveWatch is what watching *another* session adds to the poller
// (LivePlanPanel); a query panel's live tab polls its own run, which ends, so it
// needs none of it.
//
// A watch has no end of its own (it lasts until the panel closes), so it slows
// down when nobody is looking: an idle read doubles the wait up to maxBackoff as a
// failed one does, and so does a panel in a background tab (background reports
// it). wake cuts a slowed wait short, so a panel brought back to the front reads
// now rather than after up to maxBackoff.
//
// pin is the session's login time when the watch began. A session id is reused
// once its session ends, so a row from a session that logged in at another time is
// someone else's query and ends the watch with a note.
type liveWatch struct {
	pin        time.Time
	background func() bool
	wake       <-chan struct{}
}

// liveSessionEndedNote is the view's note when the watched session has ended.
func liveSessionEndedNote(spid int) string {
	return fmt.Sprintf("Session %d has ended; its id now belongs to another session, which is not shown.", spid)
}

// liveRefusedNote is the live tab's note when the server refuses the DMV. The
// run goes on regardless and still brings back its actual plan.
const liveRefusedNote = "No live statistics: reading sys.dm_exec_query_profiles needs VIEW SERVER STATE " +
	"(VIEW DATABASE STATE on Azure SQL Database). The actual plan still arrives when the query ends."

// liveUpdate is one poll's result for the UI goroutine: the statement's plan and
// its operators' counters, note when polling has stopped for good (ended too when
// that is because the watched session ended), or idle when the session is running
// no profiled statement at the moment.
type liveUpdate struct {
	plan     *showplan.Plan
	counters map[int]showplan.LiveCounters
	note     string
	ended    bool
	idle     bool
}

// liveStmtKey identifies the statement a profile row or in-flight plan belongs
// to: the plan handle and the statement's offsets in the batch.
type liveStmtKey struct {
	handle     string
	start, end int
}

func profileKey(r gosmo.QueryProfile) liveStmtKey {
	return liveStmtKey{string(r.PlanHandle), r.StatementStart, r.StatementEnd}
}

func inFlightKey(p *gosmo.InFlightPlan) liveStmtKey {
	return liveStmtKey{string(p.PlanHandle), p.StatementStart, p.StatementEnd}
}

// startLiveStats opens the Live Query Statistics tab, waiting for the first plan,
// and starts polling the panel's session until done closes (the run's goroutine
// exited) or the run is stopped (stopLiveStats). Called by startRun right after
// launch, which set p.execDone.
func (p *QueryPanel) startLiveStats(done <-chan struct{}) {
	srv := p.conn.Server
	spid := p.session.SPID()
	ctx, token := p.liveRun.Begin(srv.Context())

	p.planView = p.newPlanView()
	p.planView.SetLive(nil, nil)
	p.activeTab = 0
	p.layoutChildren()
	p.syncFocusVisuals()

	p.app.safego("polling live query statistics", func() {
		pollLiveStats(ctx, srv, spid, done, livePollTiming, nil, func(u liveUpdate) {
			p.app.postAndWake(func() { p.applyLiveUpdate(token, u) })
		})
	})
}

// applyLiveUpdate puts one poll's result on the live tab, unless the run it
// belongs to has been stopped or the tab has already become the actual plan.
func (p *QueryPanel) applyLiveUpdate(token int, u liveUpdate) {
	if !p.liveRun.Current(token) || p.planView == nil || !p.planView.Live() || u.idle {
		return // idle: between statements, or not started yet — keep the last picture
	}
	if u.note != "" {
		p.planView.SetLiveNote(u.note)
		p.app.setStatus(u.note)
		return
	}
	p.planView.SetLive(u.plan, u.counters)
}

// stopLiveStats stops the poller, cancelling a read in flight, and drops whatever
// it still has queued. The run finishing, the panel closing and a panicked run all
// come through here.
func (p *QueryPanel) stopLiveStats() {
	p.liveRun.Abandon()
}

// setRunResult is setResult for an Execute run, which may have had a live tab: it
// stops the poller first, and someone watching the live tab when the run ends
// lands on the actual plan it turns into, unless the run failed (Messages is where
// an error is read, as setResult decides).
func (p *QueryPanel) setRunResult(res *query.Result, cancelled bool) {
	onLive := p.liveTabActive()
	p.stopLiveStats()
	p.setResult(res, cancelled)
	if onLive && p.planView != nil && !res.HasErrors() {
		p.setActiveTab(len(res.Sets))
	}
}

// liveTabActive reports whether the active tab is the Live Query Statistics tab:
// the plan tab while the plan view is still live.
func (p *QueryPanel) liveTabActive() bool {
	return p.planTabActive() && p.planView.Live()
}

// pollLiveStats reads spid's operator counters every tm.every until ctx is
// cancelled or done closes (nil: never), reporting each read (idle when it found
// the session running no profiled statement). The statement's in-flight plan is
// re-read only when the counters name a different statement than the plan on
// hand: a multi-statement batch moving on, or a script's next GO batch.
//
// A refused read ends polling with a note; any other failure is retried with
// backoff, since the batch is still running and a later read may well succeed.
// watch, nil for a query panel's own run, is another session's; see liveWatch.
func pollLiveStats(ctx context.Context, src liveSource, spid int, done <-chan struct{}, tm liveTiming, watch *liveWatch, report func(liveUpdate)) {
	var (
		plan *showplan.Plan
		key  liveStmtKey
		wait = tm.first
		wake <-chan struct{}
	)
	backoff := func() { wait = min(max(wait, tm.every)*2, tm.maxBackoff) }
	if watch != nil {
		wake = watch.wake
	}
	for {
		if watch != nil && watch.background != nil && watch.background() {
			wait = max(wait, tm.maxBackoff)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-done:
			t.Stop()
			return
		case <-wake:
			t.Stop()
		case <-t.C:
		}
		rows, err := src.QueryProfiles(ctx, spid)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if gosmo.IsPermissionDenied(err) {
				report(liveUpdate{note: liveRefusedNote})
				return
			}
			backoff()
			continue
		}
		rows = currentStatementRows(rows)
		if len(rows) == 0 {
			if watch != nil {
				backoff()
			} else {
				wait = tm.every
			}
			report(liveUpdate{idle: true})
			continue
		}
		wait = tm.every
		if login := rows[0].SessionLoginTime; watch != nil && !watch.pin.IsZero() && !login.IsZero() && !login.Equal(watch.pin) {
			report(liveUpdate{note: liveSessionEndedNote(spid), ended: true})
			return
		}
		if k := profileKey(rows[0]); plan == nil || k != key {
			fp, err := src.InFlightPlan(ctx, spid)
			switch {
			case ctx.Err() != nil:
				return
			case gosmo.IsPermissionDenied(err):
				report(liveUpdate{note: liveRefusedNote})
				return
			case errors.Is(err, gosmo.ErrNotFound):
				continue // the statement ended between the two reads
			case err != nil:
				backoff()
				continue
			}
			if inFlightKey(fp) != k {
				continue // moved on between the reads; the next tick re-reads both
			}
			parsed, err := showplan.Parse([]byte(fp.XML))
			if err != nil {
				backoff()
				continue
			}
			plan, key = parsed, k
		}
		counters := showplan.MergeProfiles(profileRows(rows))
		showplan.FillPlanEstimates(plan, counters)
		report(liveUpdate{plan: plan, counters: counters})
	}
}

// currentStatementRows keeps the rows of the statement the session's first
// request is running, the one InFlightPlan reads (MARS sessions have several
// requests; QueryProfiles orders by request).
func currentStatementRows(rows []gosmo.QueryProfile) []gosmo.QueryProfile {
	if len(rows) == 0 {
		return nil
	}
	req, k := rows[0].RequestID, profileKey(rows[0])
	out := rows[:0:0]
	for _, r := range rows {
		if r.RequestID == req && profileKey(r) == k {
			out = append(out, r)
		}
	}
	return out
}

// profileRows copies the fields showplan.MergeProfiles reads; showplan stays
// free of the gosmo dependency.
func profileRows(rows []gosmo.QueryProfile) []showplan.ProfileRow {
	out := make([]showplan.ProfileRow, len(rows))
	for i, r := range rows {
		out[i] = showplan.ProfileRow{
			NodeID:           r.NodeID,
			ThreadID:         r.ThreadID,
			PhysicalOperator: r.PhysicalOperator,
			RowCount:         r.RowCount,
			EstimateRowCount: r.EstimateRowCount,
			RebindCount:      r.RebindCount,
			RewindCount:      r.RewindCount,
			EndOfScanCount:   r.EndOfScanCount,
			OpenTime:         r.OpenTime,
			CloseTime:        r.CloseTime,
			ElapsedMs:        r.ElapsedMs,
			CPUMs:            r.CPUMs,
			ScanCount:        r.ScanCount,
			LogicalReads:     r.LogicalReads,
			PhysicalReads:    r.PhysicalReads,
			ReadAheads:       r.ReadAheads,
		}
	}
	return out
}
