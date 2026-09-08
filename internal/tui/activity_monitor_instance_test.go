package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/dashboard"
)

// The Instance tab and the visible-tabs refactor it needed. The tab reads
// three views that exist only on an Azure engine edition
// (docs/plan-azure-managed-instance.md § The new Activity Monitor tab), so the
// question every test here asks is which connection sees it.

// newAzureActivityMonitor is newTestActivityMonitor against a Managed
// Instance — the fake answers the real MI's connect-time row.
func newAzureActivityMonitor(t *testing.T, w, h int) *ActivityMonitor {
	t.Helper()
	sc, _ := newFakeConnOnAzureMI(t)
	am := NewActivityMonitor(newTestApp(), sc)
	am.SetBounds(0, 0, w, h)
	return am
}

func tabLabels(am *ActivityMonitor) []string {
	tabs := am.visibleTabs()
	out := make([]string, len(tabs))
	for i, t := range tabs {
		out[i] = amTabLabels[t]
	}
	return out
}

// The tab exists on Azure and nowhere else. Drawing it on an on-premises
// server would put a permanent error strip on the panel: gosmo refuses all
// three of its reads off Azure, so it can never have anything to show.
func TestInstanceTabIsOfferedOnlyOnAzure(t *testing.T) {
	onPrem := newTestActivityMonitor(100, 40)
	want := []string{"History", "Sample", "TempDB", "Sessions", "Block"}
	if got := tabLabels(onPrem); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("on-premises tabs = %v, want %v", got, want)
	}
	if onPrem.tabVisible(amTabInstance) {
		t.Error("the Instance tab is offered on an on-premises server")
	}

	azure := newAzureActivityMonitor(t, 100, 40)
	if got := tabLabels(azure); strings.Join(got, ",") != strings.Join(append(want, "Instance"), ",") {
		t.Errorf("Managed Instance tabs = %v, want the on-premises five plus Instance", got)
	}
}

// setTab is the one gate: no key, click or caller may land the panel on a tab
// the bar does not draw, because its feed never runs there.
func TestSetTabRefusesAWithheldTab(t *testing.T) {
	am := newTestActivityMonitor(100, 40)
	am.setTab(amTabInstance)
	if am.tab == amTabInstance {
		t.Error("setTab moved to the Instance tab on an on-premises server")
	}
	am.setTab(amTab(99))
	if am.tab != amTabHistory {
		t.Errorf("setTab(99) left the panel on tab %d", am.tab)
	}
}

// Tab and Backtab walk the *visible* list. The amTab constants are not
// contiguous once one is withheld, so stepping by arithmetic over amTabCount
// would stop on a tab the bar does not draw.
func TestTabCyclingVisitsEveryVisibleTabAndNoOther(t *testing.T) {
	for _, tc := range []struct {
		name string
		am   *ActivityMonitor
	}{
		{"on-premises", newTestActivityMonitor(100, 40)},
		{"managed instance", newAzureActivityMonitor(t, 100, 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			am := tc.am
			want := am.visibleTabs()
			seen := make([]amTab, 0, len(want))
			for range want {
				seen = append(seen, am.tab)
				am.stepTab(1)
			}
			if am.tab != want[0] {
				t.Errorf("a full cycle ended on %q, not back at %q",
					amTabLabels[am.tab], amTabLabels[want[0]])
			}
			for i, tab := range want {
				if seen[i] != tab {
					t.Fatalf("step %d landed on %q, want %q", i, amTabLabels[seen[i]], amTabLabels[tab])
				}
			}
			// And backwards, which is the direction an off-by-one in the
			// modulo shows up in first.
			am.stepTab(-1)
			if am.tab != want[len(want)-1] {
				t.Errorf("Backtab from the first tab landed on %q, want the last (%q)",
					amTabLabels[am.tab], amTabLabels[want[len(want)-1]])
			}
		})
	}
}

// A click lands on the tab it looks like it landed on. The segments are
// indexed by position in visibleTabs, not by amTab, so a connection missing a
// tab must not shift every click one place.
func TestTabSegmentsPairWithTheVisibleTabs(t *testing.T) {
	for _, am := range []*ActivityMonitor{
		newTestActivityMonitor(120, 40),
		newAzureActivityMonitor(t, 120, 40),
	} {
		tabs := am.visibleTabs()
		segs := am.tabSegments()
		if len(segs) != len(tabs) {
			t.Fatalf("%d segments for %d visible tabs", len(segs), len(tabs))
		}
		for i, seg := range segs {
			am.setTab(amTabHistory)
			am.HandleMouse(tcell.NewEventMouse(seg[0].X, am.tabRect.Y, tcell.Button1, tcell.ModNone))
			am.HandleMouse(tcell.NewEventMouse(seg[0].X, am.tabRect.Y, tcell.ButtonNone, tcell.ModNone))
			if am.tab != tabs[i] {
				t.Errorf("a click on segment %d selected %q, want %q",
					i, amTabLabels[am.tab], amTabLabels[tabs[i]])
			}
		}
	}
}

// The Instance tab is a canvas tab with its own feed, at its own rates.
func TestInstanceTabHasItsOwnFeed(t *testing.T) {
	am := newAzureActivityMonitor(t, 120, 40)
	if !amTabInstance.canvasTab() {
		t.Error("the Instance tab does not draw a canvas, so every scroll key would be dead on it")
	}
	if amTabInstance.dashboardTab() {
		t.Error("the Instance tab claims the shared activity collector, whose rate and Pause are not its own")
	}
	am.setTab(amTabInstance)
	if am.feed() != &am.inst.amFeed {
		t.Fatal("the Instance tab reads another tab's feed")
	}
	if got := am.feed().rate(); got != amInstanceRates[defaultInstanceRateIdx] {
		t.Errorf("default rate = %v, want %v", got, amInstanceRates[defaultInstanceRateIdx])
	}
	// Polling faster than the source's own window cannot produce a new row.
	if amInstanceRates[0] < instanceWindow {
		t.Errorf("the fastest rate is %v, shorter than the server's own %v window",
			amInstanceRates[0], instanceWindow)
	}
}

// The plotted column is the server's window, not the panel's poll rate.
// Scaling by the poll rate would label every column with twice the span it
// covers at the default rate.
func TestInstanceColumnsKeepTheServersResolution(t *testing.T) {
	am := newAzureActivityMonitor(t, 120, 40)
	am.setTab(amTabInstance)
	am.inst.setRate(2) // 60 s
	if got := am.drawInterval(); got != instanceWindow {
		t.Errorf("drawInterval = %v at a 60 s poll rate, want the server's %v window", got, instanceWindow)
	}
	if got, want := am.resolution(), "15 sec"; got != want {
		t.Errorf("resolution = %q, want %q", got, want)
	}

	am.setTab(amTabHistory)
	if got := am.drawInterval(); got != am.act.rate() {
		t.Errorf("History's interval = %v, want its own collection rate %v", got, am.act.rate())
	}
}

// stat builds one 15-second window.
func instStat(end time.Time, cpu float64, usedMB float64, reserved int64, reqs, read, written int64) *gosmo.ServerResourceStat {
	return &gosmo.ServerResourceStat{
		StartTime: end.Add(-15 * time.Second), EndTime: end,
		SKU: "GeneralPurpose", HardwareGeneration: "Gen5", VirtualCoreCount: 4,
		AvgCPUPercent: cpu, ReservedStorageMB: reserved, StorageSpaceUsedMB: usedMB,
		IORequests: reqs, IOBytesRead: read, IOBytesWritten: written,
	}
}

// The view is built from the reading, not accumulated: every series is as long
// as the history the server returned, and the storage axis is the quota.
func TestBuildInstanceViewPlotsTheServersHistory(t *testing.T) {
	am := newAzureActivityMonitor(t, 120, 40)
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	am.inst.sample = amInstanceSample{
		At: base,
		Stats: []*gosmo.ServerResourceStat{
			instStat(base.Add(15*time.Second), 1.5, 192, 65536, 30, 0, 15360),
			instStat(base.Add(30*time.Second), 2.5, 200, 65536, 150, 1536000, 0),
		},
		Governance: &gosmo.InstanceResourceGovernance{
			CapCPU: 100, MaxWorkerThreads: 1220, MaxLogRate: 18874368,
			LocalIOPS: 6000, LocalMaxOutstandingIO: 500,
			DataDirectoryQuotaMB: 4944465, DataDirectoryUsageMB: 272,
		},
		JobObject: &gosmo.OSJobObject{
			CPURate: 400, MemoryLimitMB: 20892, ProcessMemoryLimitMB: 20892,
			WorkingSetLimitMB:   0, // NULL on a live General Purpose instance
			PeakJobMemoryUsedMB: 1462, TotalUserTime: 784687500,
			ReadOperationCount: 13992, WriteOperationCount: 26018,
		},
	}
	v := am.buildInstanceView()

	if got := len(v.CPU); got != 1 || len(v.CPU[0].Values) != 2 {
		t.Fatalf("CPU has %d series over %d buckets, want 1 over 2", got, len(v.CPU[0].Values))
	}
	if got := v.CPU[0].Values[1]; got != 2.5 {
		t.Errorf("newest CPU bucket = %v, want 2.5 — the history must be oldest first", got)
	}
	if v.StorageScale.Max != 65536 {
		t.Errorf("storage axis maximum = %v, want the 65536 MB reserved quota", v.StorageScale.Max)
	}
	// A total per 15-second window becomes a per-second rate: 150 requests
	// over 15 seconds is 10/sec, not 150.
	if got := v.IORequests[0].Values[1]; got != 10 {
		t.Errorf("IO requests/sec = %v, want 10 — the column is a total over its window", got)
	}
	if got := v.IOBytes[0].Values[1]; got != 102400 {
		t.Errorf("bytes read/sec = %v, want 102400", got)
	}
	if len(v.Times) != 2 || v.Times[1] != "12:00:30" {
		t.Errorf("Times = %v, want each bucket labelled by its end", v.Times)
	}
	if got := v.Interval; got != instanceWindow {
		t.Errorf("Interval = %v, want the server's window", got)
	}
}

func TestInstanceLimitRowsNameBothLayers(t *testing.T) {
	rows := instanceLimitRows(
		&gosmo.InstanceResourceGovernance{CapCPU: 100, MaxWorkerThreads: 1220, MaxLogRate: 18874368, LocalIOPS: 6000},
		&gosmo.OSJobObject{CPURate: 400, MemoryLimitMB: 20892, WorkingSetLimitMB: 0})
	byLabel := map[string]string{}
	for _, r := range rows {
		byLabel[r.Label] = r.Value
	}
	for label, want := range map[string]string{
		"Instance CPU cap":   "100 %",
		"Max worker threads": "1,220",
		"Local volume IOPS":  "6,000",
		"Job memory limit":   "20,892 MB",
		"Working set limit":  "not set", // NULL, and said outright rather than "0 MB"
		"Job CPU rate":       "400",
	} {
		if got := byLabel[label]; got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}

	// Neither view is fatal to the tab: the history alone still draws.
	if got := instanceLimitRows(nil, nil); len(got) != 0 {
		t.Errorf("both limit views missing produced %d rows, want none", len(got))
	}
}

// The poller never starts off Azure. Its three reads are refused there, so a
// running one would fail every tick for a tab no one can reach.
func TestInstancePollerDoesNotStartOffAzure(t *testing.T) {
	am := newTestActivityMonitor(100, 40)
	am.feedConn = &db.ServerConn{}
	am.startInstancePoller()
	if am.instPoller != nil {
		t.Error("the Instance poller started on a connection with no server info")
	}
	if am.inst.started {
		t.Error("the Instance feed reports a started collector")
	}
}

// The dashboard renders from an empty view rather than panicking: the tab is
// drawn from the moment it is selected, which is before the first tick lands.
func TestInstanceDashboardDrawsBeforeItsFirstTick(t *testing.T) {
	am := newAzureActivityMonitor(t, 120, 40)
	am.setTab(amTabInstance)
	rows := amRender(am, 120, 40)
	if !amRowsContain(rows, "Instance") {
		t.Error("the Instance tab is not on the tab bar")
	}
	for _, want := range []string{"INSTANCE", "INSTANCE STORAGE", "INSTANCE IO"} {
		if !amRowsContain(rows, want) {
			t.Errorf("the %q section is missing from an empty Instance dashboard", want)
		}
	}

	// The limits grid is the last section of a 59-row canvas, so a 40-row
	// viewport only reaches it after a scroll — which is also what proves the
	// canvas is taller than the view and the tab scrolls at all.
	if !am.scrollTo(0, dashboard.InstanceCanvasH) {
		t.Fatal("the Instance canvas does not scroll, so its last section is unreachable")
	}
	bottom := amRender(am, 120, 40)
	if !amRowsContain(bottom, "INSTANCE LIMITS") {
		t.Error("the limits section is missing from the bottom of the dashboard")
	}
	if !amRowsContain(bottom, "No limits reported yet.") {
		t.Error("an empty limits grid says nothing about being empty")
	}
}
