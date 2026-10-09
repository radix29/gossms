package planview

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ============================================================
// Live mode (Live Query Statistics)
// ============================================================
//
// Live mode: the plan is the running statement's in-flight showplan and each
// operator carries counters a poller merged from sys.dm_exec_query_profiles
// (showplan.MergeProfiles). It adds a progress row above the content, a fourth
// line to every graph tile, a rows/elapsed column to the Tree tab and live
// figures to the details panes; tiles and tree rows are coloured by state
// (running / done / not started). SetPlan or SetPlanXML (the final actual
// plan) leaves it. Nothing animates: the view repaints only when the host
// hands it new counters.

// SetLive shows p in live mode with counters keyed by NodeID of the statement
// on screen (the host has already filtered its profile rows to it). Called
// again with the same p it only swaps the counters, keeping selection, scroll
// and tab; a different p (the batch moved on) is installed fresh but keeps the
// tab. A nil p shows a waiting note until the first plan arrives.
func (v *PlanView) SetLive(p *showplan.Plan, counters map[int]showplan.LiveCounters) {
	wasLive := v.liveOn
	v.liveOn = true
	v.live = counters
	switch {
	case p == nil:
		v.plan, v.err = nil, nil
		v.layout()
	case p != v.plan:
		tab := v.activeTab
		v.installPlan(p)
		v.activeTab = tab
		v.syncFocus()
	case !wasLive:
		// Same plan entering live mode: tiles grow a line, the progress row
		// needs its place.
		v.rebuildGraphLayout()
		v.layout()
	}
}

// Live reports whether the view is in live mode (see SetLive).
func (v *PlanView) Live() bool { return v.liveOn }

// SetLiveNote replaces live mode's waiting texts (content area before a plan,
// progress row before counters) with msg, the host saying why nothing more is
// coming (the DMV was refused). "" restores them; leaving live mode clears it.
func (v *PlanView) SetLiveNote(msg string) { v.liveNote = msg }

// clearLive leaves live mode; SetPlan/SetPlanXML call it first so the actual
// plan lays out with ordinary tiles.
func (v *PlanView) clearLive() {
	v.liveOn = false
	v.live = nil
	v.liveNote = ""
}

// graphTileHeight is the tile height the current mode lays out with.
func (v *PlanView) graphTileHeight() int {
	if v.liveOn {
		return graphLiveTileH
	}
	return graphTileH
}

// liveFor returns n's live counters, if any. An operator the DMV hasn't
// reported reads as absent, not zero rows of a zero estimate.
func (v *PlanView) liveFor(n *showplan.Node) (showplan.LiveCounters, bool) {
	if !v.liveOn || n == nil {
		return showplan.LiveCounters{}, false
	}
	c, ok := v.live[n.ID]
	return c, ok
}

// liveCountersPtr is liveFor shaped for detailKVs: nil when there is nothing
// live to show.
func (v *PlanView) liveCountersPtr(n *showplan.Node) *showplan.LiveCounters {
	if c, ok := v.liveFor(n); ok {
		return &c
	}
	return nil
}

// liveStateColor is the colour of an operator's state: running in the info
// blue, done in green, not started (or not reported) dimmed.
func liveStateColor(pal *theme.Palette, c showplan.LiveCounters, ok bool) tcell.Color {
	switch {
	case !ok || c.State == showplan.LiveNotStarted:
		return pal.TextDim
	case c.State == showplan.LiveDone:
		return pal.Success
	default:
		return pal.Info
	}
}

// livePct renders progress for a "rows of estimate" figure: "over" once past
// the estimate (SSMS's word), otherwise a floored percentage.
func livePct(c showplan.LiveCounters) string {
	if c.Over() {
		return "over"
	}
	return fmt.Sprintf("%d%%", floorPct(showplan.NodeProgress(c)))
}

// floorPct turns a [0, 1] fraction into a floored whole percentage, so a capped
// 0.99 never reads 100, with slack since 0.99*100 is 98.999… in floating point.
func floorPct(f float64) int { return int(math.Floor(f*100 + 1e-9)) }

// liveRowsText is the "rows of estimate (pct)" figure, in the first form that
// fits w columns: exact counts, then K/M/B-compacted ones, then the percentage
// without parentheses, then without it. A tile is 18 columns inside, and a
// parallel exchange routinely passes millions of rows against an estimate of
// thousands, when "over" is the figure worth keeping.
func liveRowsText(c showplan.LiveCounters, w int) string {
	pct := livePct(c)
	rows, est := compactCount(c.Rows), compactCount(c.EstRows)
	for _, s := range []string{
		fmt.Sprintf("%d of %d (%s)", c.Rows, c.EstRows, pct),
		fmt.Sprintf("%s of %s (%s)", rows, est, pct),
		rows + " of " + est + " " + pct,
		rows + " of " + est,
	} {
		if core.DisplayWidth(s) <= w {
			return s
		}
	}
	return rows
}

// compactCount shortens a row count to at most 5 columns: exact below
// 10,000, then one decimal (below 100 of the unit) or none, with K, M or B.
func compactCount(n int64) string {
	unit := func(div float64, suffix string) string {
		f := float64(n) / div
		if f < 99.95 {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + suffix
		}
		return fmt.Sprintf("%.0f", f) + suffix
	}
	switch {
	case n < 10_000:
		return strconv.FormatInt(n, 10)
	case n < 999_500:
		return unit(1e3, "K")
	case n < 999_500_000:
		return unit(1e6, "M")
	default:
		return unit(1e9, "B")
	}
}

// liveElapsedText renders elapsed milliseconds as SSMS's live tiles do: seconds
// to the millisecond, minutes and seconds past the first minute.
func liveElapsedText(ms int64) string {
	if ms < 60_000 {
		return fmt.Sprintf("%.3fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm%02ds", ms/60_000, ms/1000%60)
}

// liveTimeText is liveElapsedText for an operator's ms, or a dash when the
// server did not time it (lightweight profiling, LiveCounters.Timed).
func liveTimeText(c showplan.LiveCounters, ms int64) string {
	if !c.Timed {
		return "—"
	}
	return liveElapsedText(ms)
}

// liveCPUText is an operator's CPU time, or a dash when it was not timed.
func liveCPUText(c showplan.LiveCounters) string {
	if !c.Timed {
		return "—"
	}
	return fmt.Sprintf("%d ms", c.CPUMS)
}

// liveSummary is the progress row's figures: overall completion, elapsed so far
// (the slowest operator's; the root's clock starts first and stops last), and
// operators per state. timed is false when no operator was timed (lightweight
// profiling), and the elapsed figure is then left out.
type liveSummary struct {
	progress                  float64
	elapsedMS                 int64
	timed                     bool
	running, done, notStarted int
}

func summarizeLive(m map[int]showplan.LiveCounters) liveSummary {
	s := liveSummary{progress: showplan.StatementProgress(m)}
	for _, c := range m {
		s.elapsedMS = max(s.elapsedMS, c.ElapsedMS)
		s.timed = s.timed || c.Timed
		switch c.State {
		case showplan.LiveRunning:
			s.running++
		case showplan.LiveDone:
			s.done++
		default:
			s.notStarted++
		}
	}
	return s
}

// liveBarW is the progress bar's width in cells.
const liveBarW = 20

// drawLiveRow renders live mode's progress row:
//
//	Live  ████████░░░░░░░░░░░░  42%   3.166s   2 running · 3 done · 2 not started
//
// or a waiting note before the first counters arrive.
func (v *PlanView) drawLiveRow(s tcell.Screen) {
	r := v.liveRect
	if r.H != 1 {
		return
	}
	pal := theme.Active()
	st := tcell.StyleDefault.Background(pal.PanelBg).Foreground(pal.Text)
	core.FillRect(s, r, ' ', st)
	x, right := r.X+1, r.Right()-1
	put := func(style tcell.Style, text string) {
		if w := right - x; w > 0 {
			core.DrawTextClipped(s, x, r.Y, w, style, text)
			x += min(w, core.DisplayWidth(text))
		}
	}
	put(st.Foreground(pal.TextHighlight).Bold(true), "Live  ")
	if len(v.live) == 0 {
		note := "waiting for operator counters…"
		switch {
		case v.liveNote != "" && v.plan == nil:
			note = "not available" // the content area below says why
		case v.liveNote != "":
			note = v.liveNote
		}
		put(st.Foreground(pal.TextDim), note)
		return
	}
	sum := summarizeLive(v.live)
	filled := int(sum.progress * liveBarW)
	put(st.Foreground(pal.Success), strings.Repeat("█", filled))
	put(st.Foreground(pal.Border), strings.Repeat("░", liveBarW-filled))
	put(st, fmt.Sprintf("  %d%%   ", floorPct(sum.progress)))
	if sum.timed {
		put(st, liveElapsedText(sum.elapsedMS)+"   ")
	}
	put(st.Foreground(pal.Info), fmt.Sprintf("%d running", sum.running))
	put(st, " · ")
	put(st.Foreground(pal.Success), fmt.Sprintf("%d done", sum.done))
	put(st, " · ")
	put(st.Foreground(pal.TextDim), fmt.Sprintf("%d not started", sum.notStarted))
}

// Tree tab's live column: "rows of est (pct)" then elapsed, right-aligned.
// Below liveTreeMinW the column is left out and operator text kept whole (the
// details pane has the figures).
const (
	liveTreeRowsW = 18
	liveTreeTimeW = 8
	liveTreeColW  = liveTreeRowsW + 1 + liveTreeTimeW
	liveTreeMinW  = 50
)

// liveTreeColumn is one tree row's live column text, liveTreeColW wide.
func liveTreeColumn(c showplan.LiveCounters, ok bool) string {
	if !ok {
		return core.PadRight("—", liveTreeColW)
	}
	return core.PadRight(liveRowsText(c, liveTreeRowsW), liveTreeRowsW) + " " +
		fmt.Sprintf("%*s", liveTreeTimeW, liveTimeText(c, c.ElapsedMS))
}

// liveKVs is the details panes' live block for one operator.
func liveKVs(c showplan.LiveCounters) []showplan.KV {
	return []showplan.KV{
		{Key: "Live State", Value: c.State.String()},
		{Key: "Live Rows", Value: liveRowsText(c, math.MaxInt)},
		{Key: "Live Elapsed", Value: liveTimeText(c, c.ElapsedMS)},
		{Key: "Live CPU", Value: liveCPUText(c)},
		{Key: "Live Logical Reads", Value: strconv.FormatInt(c.LogicalReads, 10)},
	}
}

// drawLiveTileLines draws a live tile's two figure lines below its name and
// object: cost and elapsed (plus the parallel mark), then rows of estimate.
// An operator the DMV has not reported shows its cost alone and a dash.
func (v *PlanView) drawLiveTileLines(s tcell.Screen, inner core.Rect, style tcell.Style, n *showplan.Node, costPct float64, c showplan.LiveCounters, ok bool) {
	metrics := fmt.Sprintf("%.0f%%", costPct*100)
	if ok && c.State != showplan.LiveNotStarted && c.Timed {
		metrics += "  " + liveElapsedText(c.ElapsedMS)
	}
	if n.Parallel {
		metrics += "  ⇄"
	}
	core.DrawTextClipped(s, inner.X, inner.Y+2, inner.W, style, metrics)
	rows := "—"
	if ok {
		rows = liveRowsText(c, inner.W)
	}
	core.DrawTextClipped(s, inner.X, inner.Y+3, inner.W, style, rows)
}
