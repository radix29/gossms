package planview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// liveScreen is a mock terminal the size of the views below, so a test can
// read back what a frame drew.
func liveScreen(t *testing.T, w, h int) tcell.Screen {
	t.Helper()
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: vt.Col(w), Y: vt.Row(h)}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	return s
}

// screenLine is row y of s as text, wide glyphs' trailing cells skipped.
func screenLine(s tcell.Screen, y int) string {
	w, _ := s.Size()
	var sb strings.Builder
	for x := 0; x < w; {
		str, _, cw := s.Get(x, y)
		sb.WriteString(str)
		x += max(cw, 1)
	}
	return sb.String()
}

// findOnScreen returns the first cell where text starts, or -1, -1.
func findOnScreen(s tcell.Screen, text string) (int, int) {
	_, h := s.Size()
	for y := range h {
		line := screenLine(s, y)
		if i := strings.Index(line, text); i >= 0 {
			return core.DisplayWidth(line[:i]), y
		}
	}
	return -1, -1
}

// liveCountersFor gives every operator of p's first statement except the
// root counters — the root left unreported, as Compute Scalars are by the
// DMV. Operators alternate done / running; the running ones sit at half
// their estimate.
func liveCountersFor(p *showplan.Plan) map[int]showplan.LiveCounters {
	st := p.Statements[0]
	m := map[int]showplan.LiveCounters{}
	for i, n := range st.Nodes() {
		if n == st.Root {
			continue
		}
		c := showplan.LiveCounters{NodeID: n.ID, PhysicalOp: n.PhysicalOp, EstRows: 1000, ElapsedMS: 1234, Timed: true}
		if i%2 == 0 {
			c.State, c.Rows = showplan.LiveDone, 1000
		} else {
			c.State, c.Rows = showplan.LiveRunning, 500
		}
		m[n.ID] = c
	}
	return m
}

// TestSetLiveKeepsTheViewAcrossPolls: new counters for the same plan repaint
// without moving the user's tab or selection; the next statement's plan resets
// the selection but keeps the tab; the actual plan arriving leaves live mode.
func TestSetLiveKeepsTheViewAcrossPolls(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 160, 40)
	p := loadTestPlan(t)
	v.SetLive(p, liveCountersFor(p))
	if !v.Live() || v.liveRect.H != 1 {
		t.Fatalf("after SetLive: Live() = %v, liveRect %+v; want live with a progress row", v.Live(), v.liveRect)
	}
	if h := v.graphSt.layout.tiles[0].rect.H; h != graphLiveTileH {
		t.Errorf("live tile height = %d, want %d", h, graphLiveTileH)
	}
	if v.contentRect.Y != v.liveRect.Y+1 {
		t.Errorf("content starts at row %d, want right under the progress row (%d)", v.contentRect.Y, v.liveRect.Y+1)
	}

	v.setActiveTab(TabTree)
	v.moveTreeSelection(2)
	sel := v.selectedID
	v.SetLive(p, liveCountersFor(p))
	if v.activeTab != TabTree || v.selectedID != sel {
		t.Errorf("a counters update moved the view: tab %v selection %d, want Tree / %d", v.activeTab, v.selectedID, sel)
	}

	next := loadTestPlan(t)
	v.SetLive(next, nil)
	if v.activeTab != TabTree {
		t.Errorf("a new statement's plan switched tab to %v, want Tree kept", v.activeTab)
	}
	if v.Plan() != next || v.selectedID != next.Statements[0].Root.ID {
		t.Errorf("new plan: selection %d, want the new root %d", v.selectedID, next.Statements[0].Root.ID)
	}

	v.SetPlan(loadTestPlan(t))
	if v.Live() || v.liveRect != (core.Rect{}) {
		t.Errorf("SetPlan left live mode on: Live() = %v, liveRect %+v", v.Live(), v.liveRect)
	}
	if h := v.graphSt.layout.tiles[0].rect.H; h != graphTileH {
		t.Errorf("tile height after SetPlan = %d, want %d", h, graphTileH)
	}
	if v.activeTab != TabPlan {
		t.Errorf("the actual plan opened on %v, want Plan as any SetPlan does", v.activeTab)
	}
}

// TestLiveTilesShowRowsOfEstimateByState draws a live frame: a reported
// operator's tile gains its rows-of-estimate line and a state-coloured
// border; the unreported root shows a dash.
func TestLiveTilesShowRowsOfEstimateByState(t *testing.T) {
	const w, h = 160, 40
	s := liveScreen(t, w, h)
	v := New()
	v.SetBounds(0, 0, w, h)
	p := loadTestPlan(t)
	live := liveCountersFor(p)
	v.SetLive(p, live)
	v.Draw(s)

	if x, _ := findOnScreen(s, "Live  "); x < 0 {
		t.Error("no progress row drawn")
	}
	if x, _ := findOnScreen(s, "500 of 1000 (50%)"); x < 0 {
		t.Error("no running tile shows \"500 of 1000 (50%)\"")
	}
	if x, _ := findOnScreen(s, "1000 of 1000 (100%)"); x >= 0 {
		t.Error("a done tile's 19-column figure should have been compacted to fit 18")
	}

	pal := theme.Active()
	var sawRunning, sawDone bool
	for _, tl := range v.graphSt.layout.tiles {
		r := v.graphCanvasRect
		x, y := r.X+tl.rect.X-v.graphSt.scrollX, r.Y+tl.rect.Y-v.graphSt.scrollY
		if !rectFullyIn(r, core.Rect{X: x, Y: y, W: tl.rect.W, H: tl.rect.H}) || tl.node.ID == v.selectedID {
			continue
		}
		_, st, _ := s.Get(x, y)
		c, ok := live[tl.node.ID]
		switch {
		case !ok:
			t.Errorf("unreported operator %d drawn as a tile other than the selected root", tl.node.ID)
		case c.State == showplan.LiveRunning:
			sawRunning = true
			if st.GetForeground() != pal.Info {
				t.Errorf("running operator %d border %v, want Info", tl.node.ID, st.GetForeground())
			}
		case c.State == showplan.LiveDone:
			sawDone = true
			if st.GetForeground() != pal.Success {
				t.Errorf("done operator %d border %v, want Success", tl.node.ID, st.GetForeground())
			}
		}
		if got := screenLine(s, y+4)[x+1 : x+1+len("—")]; !ok && got != "—" {
			t.Errorf("unreported operator %d rows line %q, want a dash", tl.node.ID, got)
		}
	}
	if !sawRunning || !sawDone {
		t.Fatalf("frame showed running %v, done %v; the fixture should give both on screen", sawRunning, sawDone)
	}
}

// TestLiveTreeColumn: the Tree tab draws each operator's live figures in a
// right-hand column, a dash for one the DMV has not reported, and drops the
// column in a pane too narrow to keep the operator text readable.
func TestLiveTreeColumn(t *testing.T) {
	const w, h = 160, 40
	s := liveScreen(t, w, h)
	v := New()
	v.SetBounds(0, 0, w, h)
	p := loadTestPlan(t)
	v.SetLive(p, liveCountersFor(p))
	v.setActiveTab(TabTree)
	v.Draw(s)

	r := v.treePaneRect
	for i, tr := range v.treeSt.rows {
		if i >= r.H {
			break
		}
		c, ok := v.liveFor(tr.node)
		want := liveTreeColumn(c, ok)
		line := screenLine(s, r.Y+i)
		if !strings.Contains(line, strings.TrimRight(want, " ")) {
			t.Errorf("tree row %d (%s): live column %q missing from %q", i, tr.node.PhysicalOp, want, line)
		}
	}
	if x, _ := findOnScreen(s, "Elapsed: "); x < 0 {
		t.Fatal("tree header not drawn")
	} else if _, y := findOnScreen(s, "Elapsed: —"); y < 0 {
		t.Error("tree header shows the in-flight plan's stale elapsed time in live mode, want a dash")
	}

	v.treeSplit.SetRatio(0.2) // pane well under liveTreeMinW
	v.layout()
	if v.treePaneRect.W >= liveTreeMinW {
		t.Fatalf("tree pane %d wide, test needs it under %d", v.treePaneRect.W, liveTreeMinW)
	}
	v.Draw(s)
	if x, _ := findOnScreen(s, "500 of 1000"); x >= 0 && x < v.treePaneRect.Right() {
		t.Error("a narrow tree pane still drew the live column")
	}
}

// TestLiveDetailsReplaceTheActualFigures: in live mode the details carry the
// live block and drop the in-flight plan's partial Actual rows/CPU/duration.
func TestLiveDetailsReplaceTheActualFigures(t *testing.T) {
	p := loadTestPlan(t)
	st := p.Statements[0]
	var n *showplan.Node
	for _, c := range st.Nodes() {
		if c.Runtime != nil && c != st.Root {
			n = c
			break
		}
	}
	if n == nil {
		t.Fatal("fixture has no non-root operator with runtime counters")
	}
	live := showplan.LiveCounters{State: showplan.LiveRunning, Rows: 7781283, EstRows: 36000000, ElapsedMS: 898, CPUMS: 870, Threads: 4, Timed: true}
	text := strings.Join(detailLines(n, st, &live), "\n")
	for _, want := range []string{"Live State", "running", "7781283 of 36000000 (21%)", "0.898s", "870 ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("live details lack %q:\n%s", want, text)
		}
	}
	for _, gone := range []string{"Actual Rows", "Actual CPU", "Actual Duration"} {
		if strings.Contains(text, gone) {
			t.Errorf("live details still show %q:\n%s", gone, text)
		}
	}
	if !strings.Contains(strings.Join(detailLines(n, st, nil), "\n"), "Actual Rows") {
		t.Error("without live counters the Actual figures should be back")
	}
}

func TestSetLiveWithoutAPlanWaits(t *testing.T) {
	s := liveScreen(t, 80, 10)
	v := New()
	v.SetBounds(0, 0, 80, 10)
	v.SetLive(nil, nil)
	v.Draw(s)
	if x, _ := findOnScreen(s, "Waiting for the running statement's plan"); x < 0 {
		t.Error("no waiting note before the first plan")
	}
	if x, _ := findOnScreen(s, "waiting for operator counters"); x < 0 {
		t.Error("progress row should say it has no counters yet")
	}
}

// The host's note replaces the waiting texts: wrapped in full in the content
// area (it is longer than a line), only "not available" on the progress row
// so it is not said twice. Leaving live mode drops it.
func TestSetLiveNoteExplainsWhyNothingIsComing(t *testing.T) {
	s := liveScreen(t, 40, 10)
	v := New()
	v.SetBounds(0, 0, 40, 10)
	v.SetLive(nil, nil)
	note := "No live statistics: the server refused the read. The actual plan still arrives."
	v.SetLiveNote(note)
	v.Draw(s)
	if x, _ := findOnScreen(s, "Waiting for the running"); x >= 0 {
		t.Error("the waiting note is still shown under the host's note")
	}
	if x, _ := findOnScreen(s, "not available"); x < 0 {
		t.Error("progress row should say live figures are not available")
	}
	if x, _ := findOnScreen(s, "arrives."); x < 0 {
		t.Error("the note's end is not on screen — it was clipped, not wrapped")
	}

	v.SetPlanXML(twoStatementPlanXML)
	v.SetLive(v.Plan(), nil)
	if v.liveNote != "" {
		t.Errorf("liveNote = %q after leaving live mode, want it cleared", v.liveNote)
	}
}

func TestLiveRowsText(t *testing.T) {
	over := showplan.LiveCounters{State: showplan.LiveRunning, Rows: 8478846, EstRows: 3000}
	capped := showplan.LiveCounters{State: showplan.LiveRunning, Rows: 3000, EstRows: 3000}
	done := showplan.LiveCounters{State: showplan.LiveDone, Rows: 3, EstRows: 10}
	for _, tc := range []struct {
		c    showplan.LiveCounters
		w    int
		want string
	}{
		{over, 100, "8478846 of 3000 (over)"},
		{over, 18, "8.5M of 3000 over"},
		{showplan.LiveCounters{State: showplan.LiveRunning, Rows: 78_812, EstRows: 2926}, 18, "78.8K of 2926 over"},
		{over, 16, "8.5M of 3000"},
		{over, 10, "8.5M"},
		{capped, 18, "3000 of 3000 (99%)"},
		{done, 18, "3 of 10 (100%)"},
		{showplan.LiveCounters{EstRows: 50}, 18, "0 of 50 (0%)"},
	} {
		if got := liveRowsText(tc.c, tc.w); got != tc.want {
			t.Errorf("liveRowsText(%+v, %d) = %q, want %q", tc.c, tc.w, got, tc.want)
		}
	}
}

func TestCompactCount(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0"}, {9999, "9999"}, {10_000, "10K"}, {12_345, "12.3K"}, {999_499, "999K"},
		{999_500, "1M"}, {36_000_000, "36M"}, {8_478_846, "8.5M"}, {1_500_000_000, "1.5B"},
	} {
		if got := compactCount(tc.n); got != tc.want {
			t.Errorf("compactCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestLiveElapsedText(t *testing.T) {
	for ms, want := range map[int64]string{0: "0.000s", 898: "0.898s", 59_999: "59.999s", 61_500: "1m01s", 3_600_000: "60m00s"} {
		if got := liveElapsedText(ms); got != want {
			t.Errorf("liveElapsedText(%d) = %q, want %q", ms, got, want)
		}
	}
}

// TestSetLiveOnTheShownPlan: handing SetLive the plan already on screen still
// enters live mode — the tiles grow their figures line and the progress row
// takes its place.
func TestSetLiveOnTheShownPlan(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 160, 40)
	p := loadTestPlan(t)
	v.SetPlan(p)
	v.SetLive(p, liveCountersFor(p))
	if v.liveRect.H != 1 || v.graphSt.layout.tiles[0].rect.H != graphLiveTileH {
		t.Errorf("liveRect %+v, tile height %d; want a progress row and %d-row tiles",
			v.liveRect, v.graphSt.layout.tiles[0].rect.H, graphLiveTileH)
	}
}

// An operator lightweight profiling counted but did not time shows dashes,
// not a measured 0.
func TestLiveUntimedShowsDashes(t *testing.T) {
	c := showplan.LiveCounters{State: showplan.LiveRunning, Rows: 5, EstRows: 10}
	if got := strings.TrimSpace(liveTreeColumn(c, true)); !strings.HasSuffix(got, "—") {
		t.Errorf("tree column = %q, want a dash for elapsed", got)
	}
	for _, kv := range liveKVs(c) {
		if (kv.Key == "Live Elapsed" || kv.Key == "Live CPU") && kv.Value != "—" {
			t.Errorf("%s = %q, want —", kv.Key, kv.Value)
		}
	}
	if summarizeLive(map[int]showplan.LiveCounters{1: c}).timed {
		t.Error("summary of untimed counters claims a time")
	}
}
