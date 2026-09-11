package tui

import (
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// Estimated data loss and recovery time are derived here, not reported by SQL
// Server, and a wrong value is one a DBA would act on.

func TestComputeDatabaseMetricsDataLossIsMeasuredAgainstThePrimary(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	dbs := []*gosmo.AvailabilityDatabase{
		{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true, LastCommitTime: base},
		{DatabaseName: "db1", ReplicaServerName: "s", LastCommitTime: base.Add(-45 * time.Second)},
	}
	got := agComputeDatabaseMetrics(dbs)

	// The primary's data loss stays blank, not "the primary is losing data".
	if got[0].HasDataLoss {
		t.Errorf("primary row reports data loss %v", got[0].DataLoss)
	}
	if !got[1].HasDataLoss || got[1].DataLoss != 45*time.Second {
		t.Errorf("secondary data loss = %v (known=%v), want 45s", got[1].DataLoss, got[1].HasDataLoss)
	}
}

func TestComputeDatabaseMetricsDataLossEdgeCases(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	// A secondary commit after the primary's is clock skew, not negative loss.
	ahead := agComputeDatabaseMetrics([]*gosmo.AvailabilityDatabase{
		{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true, LastCommitTime: base},
		{DatabaseName: "db1", ReplicaServerName: "s", LastCommitTime: base.Add(2 * time.Second)},
	})
	if !ahead[1].HasDataLoss || ahead[1].DataLoss != 0 {
		t.Errorf("secondary ahead of primary = %v, want a clamped 0", ahead[1].DataLoss)
	}

	// No commit time on either side is unknown, not zero.
	for _, tt := range []struct {
		name string
		dbs  []*gosmo.AvailabilityDatabase
	}{
		{"secondary has no commit time", []*gosmo.AvailabilityDatabase{
			{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true, LastCommitTime: base},
			{DatabaseName: "db1", ReplicaServerName: "s"},
		}},
		{"primary has no commit time", []*gosmo.AvailabilityDatabase{
			{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true},
			{DatabaseName: "db1", ReplicaServerName: "s", LastCommitTime: base},
		}},
		{"no primary row at all", []*gosmo.AvailabilityDatabase{
			{DatabaseName: "db1", ReplicaServerName: "s", LastCommitTime: base},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := agComputeDatabaseMetrics(tt.dbs)
			last := got[len(got)-1]
			if last.HasDataLoss {
				t.Errorf("data loss reported as %v, want unknown", last.DataLoss)
			}
		})
	}

	// Data loss is per database; another database's primary mustn't supply the
	// reference.
	cross := agComputeDatabaseMetrics([]*gosmo.AvailabilityDatabase{
		{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true, LastCommitTime: base},
		{DatabaseName: "db2", ReplicaServerName: "s", LastCommitTime: base.Add(-90 * time.Second)},
	})
	if cross[1].HasDataLoss {
		t.Error("db2's secondary took its reference commit time from db1's primary")
	}
}

func TestComputeDatabaseMetricsRecoveryTime(t *testing.T) {
	tests := []struct {
		name      string
		redoQueue int64
		redoRate  int64
		want      time.Duration
		known     bool
	}{
		{"draining", 1000, 250, 4 * time.Second, true},
		// Caught up (rate 0, queue 0) is a known zero; blank would look like a
		// stall.
		{"nothing queued", 0, 0, 0, true},
		// A queue with no rate can't be turned into a time: unknown, not
		// instant.
		{"queued but no rate", 5000, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agComputeDatabaseMetrics([]*gosmo.AvailabilityDatabase{
				{DatabaseName: "db1", ReplicaServerName: "s", RedoQueueKB: tt.redoQueue, RedoRateKBps: tt.redoRate},
			})[0]
			if got.HasRecoveryTime != tt.known {
				t.Fatalf("known = %v, want %v", got.HasRecoveryTime, tt.known)
			}
			if tt.known && got.RecoveryTime != tt.want {
				t.Errorf("recovery time = %v, want %v", got.RecoveryTime, tt.want)
			}
		})
	}

	// The primary has no redo queue.
	primary := agComputeDatabaseMetrics([]*gosmo.AvailabilityDatabase{
		{DatabaseName: "db1", ReplicaServerName: "p", IsPrimaryReplica: true, RedoQueueKB: 900, RedoRateKBps: 300},
	})[0]
	if primary.HasRecoveryTime {
		t.Errorf("primary row reports a recovery time of %v", primary.RecoveryTime)
	}
}

func TestAGDurationRendersUnknownAsADash(t *testing.T) {
	if got := agDuration(0, false); got != "—" {
		t.Errorf("unknown duration = %q, want an em dash — %q would claim a fact", got, got)
	}
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{500 * time.Millisecond, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m 30s"},
		{3*time.Hour + 4*time.Minute, "3h 04m"},
	} {
		if got := agDuration(tt.d, true); got != tt.want {
			t.Errorf("agDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestReplicaIssuesNamesWhatIsWrong(t *testing.T) {
	healthy := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql2", ConnectedState: "CONNECTED",
		RecoveryHealth: "ONLINE", SynchronizationHealth: "HEALTHY",
	}
	if got := agReplicaIssues(healthy, nil); got != "" {
		t.Errorf("healthy replica reports issues %q", got)
	}

	// A replica with no state row (a secondary's view of peers) has empty
	// fields, not an issue.
	if got := agReplicaIssues(&gosmo.AvailabilityReplica{ReplicaServerName: "ubusql1"}, nil); got != "" {
		t.Errorf("replica with no DMV state reports issues %q", got)
	}

	disconnected := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql2", ConnectedState: "DISCONNECTED",
		SynchronizationHealth: "NOT_HEALTHY",
	}
	got := agReplicaIssues(disconnected, nil)
	if !strings.HasPrefix(got, "Disconnected") {
		t.Errorf("issues = %q, want the connection state first — it explains everything below it", got)
	}
	if !strings.Contains(got, "Not healthy") {
		t.Errorf("issues = %q, want the synchronization health too", got)
	}

	suspended := agReplicaIssues(healthy, []agDatabaseMetrics{
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql2", IsSuspended: true}},
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql2"}},
		// Another replica's suspended database doesn't count.
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql1", IsSuspended: true}},
	})
	if suspended != "1 database(s) suspended" {
		t.Errorf("issues = %q, want exactly one suspended database counted", suspended)
	}

	// An old connect error is reported only when nothing current is.
	stale := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql2", ConnectedState: "DISCONNECTED", LastConnectErrorNumber: 35206,
	}
	if got := agReplicaIssues(stale, nil); strings.Contains(got, "35206") {
		t.Errorf("issues = %q, want the live problem alone while one exists", got)
	}
	quiet := &gosmo.AvailabilityReplica{
		ReplicaServerName: "ubusql2", ConnectedState: "CONNECTED", LastConnectErrorNumber: 35206,
	}
	if got := agReplicaIssues(quiet, nil); !strings.Contains(got, "35206") {
		t.Errorf("issues = %q, want the last connect error when nothing else is wrong", got)
	}
}

func TestReplicaSyncSummaryListsEveryDistinctState(t *testing.T) {
	r := &gosmo.AvailabilityReplica{ReplicaServerName: "ubusql2"}
	dbs := []agDatabaseMetrics{
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql2", SynchronizationState: "SYNCHRONIZED"}},
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql2", SynchronizationState: "SYNCHRONIZING"}},
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql2", SynchronizationState: "SYNCHRONIZED"}},
		{DB: &gosmo.AvailabilityDatabase{ReplicaServerName: "ubusql1", SynchronizationState: "NOT SYNCHRONIZING"}},
	}
	if got, want := agReplicaSyncSummary(r, dbs), "Synchronized, Synchronizing"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// Same-shape updates must not call SetData, which resets scroll every poll.
func TestSetRowsPreservesScrollWhenTheShapeIsUnchanged(t *testing.T) {
	d := &AGDashboard{topGrid: controls.NewDataGrid()}
	d.topGrid.SetBounds(0, 0, 80, 8)

	first := make([][]string, 20)
	for i := range first {
		first[i] = []string{"r", "old"}
	}
	d.setRows(d.topGrid, &d.topRows, []string{"Replica", "State"}, first)
	d.topGrid.SetSelectedRow(15)
	scroll, sel := d.topGrid.ScrollRow(), d.topGrid.SelectedRow()
	if scroll == 0 {
		t.Fatal("test setup: selecting row 15 in an 8-row grid should have scrolled")
	}

	second := make([][]string, 20)
	for i := range second {
		second[i] = []string{"r", "new"}
	}
	d.setRows(d.topGrid, &d.topRows, []string{"Replica", "State"}, second)

	if got := d.topGrid.ScrollRow(); got != scroll {
		t.Errorf("scroll moved from %d to %d on a same-shape refresh", scroll, got)
	}
	if got := d.topGrid.SelectedRow(); got != sel {
		t.Errorf("selection moved from %d to %d on a same-shape refresh", sel, got)
	}
	if got := d.topGrid.Row(15)[1]; got != "new" {
		t.Errorf("row 15 still reads %q — the refresh did not reach the grid", got)
	}

	// A shape change (replica joins/leaves) does use SetData.
	d.setRows(d.topGrid, &d.topRows, []string{"Replica", "State"}, second[:5])
	if got := d.topGrid.ScrollRow(); got != 0 {
		t.Errorf("scroll = %d after the row count changed, want a reset to 0", got)
	}
}

// The dashboard is reachable only from the AG node's context menu.
func TestAvailabilityGroupNodeOffersDashboard(t *testing.T) {
	a := &App{}
	node := &explorerNode{}
	node.data.Type = NodeAvailabilityGroup
	node.data.AGName = "AAG1"

	var labels []string
	for _, item := range a.contextMenuItemsForNode(node) {
		labels = append(labels, item.Label)
	}
	if !slicesContains(labels, "Show Dashboard") {
		t.Errorf("availability group context menu = %v, want a Show Dashboard item", labels)
	}
}

func TestAGLabelForDatabase(t *testing.T) {
	states := map[string]agDatabaseSummary{
		"testdb_1": {Name: "testdb_1", States: []string{"SYNCHRONIZED"}},
		"testdb_2": {Name: "testdb_2", States: []string{"NOT SYNCHRONIZING"}, Suspended: true},
	}
	if got, want := agLabelForDatabase("testdb_1", states), "testdb_1 (Synchronized)"; got != want {
		t.Errorf("agLabelForDatabase = %q, want %q", got, want)
	}
	// Case-insensitive: sys.databases and the AG views can disagree on case.
	if got, want := agLabelForDatabase("TESTDB_1", states), "TESTDB_1 (Synchronized)"; got != want {
		t.Errorf("agLabelForDatabase with different case = %q, want %q", got, want)
	}
	if got := agLabelForDatabase("testdb_2", states); !strings.Contains(got, "Suspended") {
		t.Errorf("suspended database label = %q, want it to say so", got)
	}
	// A database in no group keeps its plain name, as do all when there are no
	// groups (nil states).
	if got, want := agLabelForDatabase("other", states), "other"; got != want {
		t.Errorf("non-AG database = %q, want %q", got, want)
	}
	if got, want := agLabelForDatabase("testdb_1", nil), "testdb_1"; got != want {
		t.Errorf("with no AG states = %q, want %q", got, want)
	}
}

// The replica grid is sized from its rows, but first laid out with none;
// without a re-split on apply, a two-replica group showed one — cutting the
// primary's row.
func TestApplyResizesTheReplicaGridToFitEveryReplica(t *testing.T) {
	// agName must be set; empty means the all-groups view.
	d := &AGDashboard{agName: "AAG1", topGrid: controls.NewDataGrid(), bottomGrid: controls.NewDataGrid()}
	d.SetBounds(0, 0, 120, 40)
	before := d.topRect.H

	replicas := []*gosmo.AvailabilityReplica{
		{ReplicaServerName: "a", Role: "PRIMARY"},
		{ReplicaServerName: "b", Role: "SECONDARY"},
		{ReplicaServerName: "c", Role: "SECONDARY"},
		{ReplicaServerName: "d", Role: "SECONDARY"},
	}
	d.apply(agSnapshot{group: &gosmo.AvailabilityGroup{Name: "AAG1"}, replicas: replicas, at: time.Now()}, nil)

	if d.topRect.H < len(replicas)+agGridChrome {
		t.Errorf("replica grid is %d high for %d replicas (was %d before the reading); it needs %d",
			d.topRect.H, len(replicas), before, len(replicas)+agGridChrome)
	}
	if d.bottomRect.H <= 0 {
		t.Error("the database grid was squeezed out entirely")
	}
}

// -- all-groups view -------------------------------------------------------

func TestAllGroupsViewSwapsBothGridsContents(t *testing.T) {
	// Grids are named by position because their contents depend on mode; a
	// wrong pairing puts replica rows under group headers silently.
	one := &AGDashboard{agName: "AAG1"}
	all := &AGDashboard{}

	if all.allGroups() == one.allGroups() {
		t.Fatal("an empty agName must be the all-groups view and a set one must not")
	}
	if got := len(one.topColumns()); got != len(agReplicaColumns) {
		t.Errorf("one-group top grid has %d columns, want the replica columns", got)
	}
	if got := len(all.topColumns()); got != len(agGroupColumns) {
		t.Errorf("all-groups top grid has %d columns, want the group columns", got)
	}
	if len(all.bottomColumns()) != len(agReplicaColumns)+1 {
		t.Errorf("all-groups bottom grid has %d columns, want the replica columns plus the group name",
			len(all.bottomColumns()))
	}
	// Every row must match its header's width, or trailing cells are dropped.
	snap := agSnapshot{allGroup: true, groups: []agGroupRollup{{
		group:    &gosmo.AvailabilityGroup{Name: "AAG1", SynchronizationHealth: "HEALTHY"},
		replicas: []*gosmo.AvailabilityReplica{{ReplicaServerName: "a", ConnectedState: "CONNECTED"}},
	}}}
	for _, row := range all.topRowsFrom(snap) {
		if len(row) != len(agGroupColumns) {
			t.Errorf("group row has %d cells, want %d", len(row), len(agGroupColumns))
		}
	}
	for _, row := range all.bottomRowsFrom(snap) {
		if len(row) != len(all.bottomColumns()) {
			t.Errorf("replica row has %d cells, want %d", len(row), len(all.bottomColumns()))
		}
		if row[0] != "AAG1" {
			t.Errorf("replica row's first cell = %q, want the group it belongs to", row[0])
		}
	}
}

func TestGroupRollupIssuesLeadsWithAnUnreachablePrimary(t *testing.T) {
	// An unreachable primary replaces the other issues rather than joining
	// them.
	g := agGroupRollup{
		group:       &gosmo.AvailabilityGroup{Name: "AAG1", SynchronizationHealth: "NOT_HEALTHY"},
		unreachable: "ubusql2",
		replicas:    []*gosmo.AvailabilityReplica{{ReplicaServerName: "a", ConnectedState: "DISCONNECTED"}},
	}
	got := g.issues()
	if !strings.Contains(got, "ubusql2") || !strings.HasPrefix(got, "Partial") {
		t.Errorf("issues = %q, want it to lead with the unreachable primary", got)
	}
	if strings.Contains(got, "disconnected") {
		t.Errorf("issues = %q, want the consequences of not reaching the primary left out", got)
	}

	// A healthy group shows nothing.
	healthy := agGroupRollup{
		group:    &gosmo.AvailabilityGroup{Name: "AAG1", SynchronizationHealth: "HEALTHY"},
		replicas: []*gosmo.AvailabilityReplica{{ReplicaServerName: "a", ConnectedState: "CONNECTED"}},
	}
	if got := healthy.issues(); got != "" {
		t.Errorf("healthy group reports issues %q, want none", got)
	}

	// A suspended copy is an issue even though the group reports healthy
	// (suspension is a user action).
	suspended := agGroupRollup{
		group: &gosmo.AvailabilityGroup{Name: "AAG1", SynchronizationHealth: "HEALTHY"},
		dbs: []agDatabaseMetrics{
			{DB: &gosmo.AvailabilityDatabase{DatabaseName: "db1", IsSuspended: true}},
		},
	}
	if got := suspended.issues(); !strings.Contains(got, "suspended") {
		t.Errorf("issues = %q, want the suspended copy named", got)
	}
}

func TestDashboardRateSelectorStopsAtBothEnds(t *testing.T) {
	// setRate's result is HandleKey's; true at the list end would swallow +/-.
	d := &AGDashboard{agName: "AAG1", rateCh: make(chan struct{}, 1)}
	d.rateIdx.Store(agDashboardDefaultRate)

	if !d.setRate(0) {
		t.Fatal("setRate(0) refused a valid index")
	}
	if d.rate() != agDashboardRates[0] {
		t.Errorf("rate = %v, want %v", d.rate(), agDashboardRates[0])
	}
	if d.setRate(-1) {
		t.Error("setRate(-1) accepted an index before the start of the list")
	}
	if d.setRate(len(agDashboardRates)) {
		t.Error("setRate accepted an index past the end of the list")
	}
	if d.rate() != agDashboardRates[0] {
		t.Errorf("a refused index changed the rate to %v", d.rate())
	}
	if len(agDashboardRateLabels) != len(agDashboardRates) {
		t.Fatalf("%d labels for %d rates — the header indexes one by the other",
			len(agDashboardRateLabels), len(agDashboardRates))
	}
}
