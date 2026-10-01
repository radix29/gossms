package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// The Object Explorer and Details wiring for Management ▸ Resource Governor.
// Statement text is gosmo's to test; these pin what the tree and the pane do
// with the answers — the edition gate, the state label, "not visible" versus
// "none", groups under their own pool, and live counters that go blank rather
// than failing the view.

// Match text for gosmo's Resource Governor reads. The pool list is matched
// with its ORDER BY, since the group read joins the same catalog view.
const (
	rgConfigRead    = "FROM   sys.resource_governor_configuration"
	rgStatusRead    = "sys.dm_resource_governor_configuration"
	rgPoolList      = "FROM   sys.resource_governor_resource_pools\nORDER  BY name"
	rgPoolAffinity  = "sys.resource_governor_resource_pool_affinity"
	rgGroupList     = "FROM   sys.resource_governor_workload_groups g"
	rgPoolStatsRead = "sys.dm_resource_governor_resource_pools"
	rgGrpStatsRead  = "sys.dm_resource_governor_workload_groups"
)

// rgConfig answers the stored configuration: enabled, no classifier.
func rgConfig(enabled bool) fakeResponse {
	return fakeResponse{match: rgConfigRead, cols: 5, rows: [][]driver.Value{{enabled, int64(0), nil, nil, int64(0)}}}
}

// rgStatus answers the configuration in force.
func rgStatus(pending bool) fakeResponse {
	return fakeResponse{match: rgStatusRead, cols: 5, rows: [][]driver.Value{{int64(0), nil, nil, pending, int64(0)}}}
}

// rgPools answers the pool list: internal, default and a user pool, plus an
// empty affinity read.
func rgPools() []fakeResponse {
	return []fakeResponse{
		{match: rgPoolList, cols: 9, rows: [][]driver.Value{
			{int64(2), "default", int64(0), int64(100), int64(100), int64(0), int64(100), int64(0), int64(0)},
			{int64(1), "internal", int64(0), int64(100), int64(100), int64(0), int64(100), int64(0), int64(0)},
			{int64(256), "reports", int64(5), int64(40), int64(50), int64(0), int64(30), int64(0), int64(0)},
		}},
		{match: rgPoolAffinity, cols: 3},
	}
}

// rgGroup is one row of the workload group read.
func rgGroup(id int64, name string, poolID int64, pool string) []driver.Value {
	return []driver.Value{id, name, poolID, pool, int64(2), "default", "Medium", float64(25),
		int64(0), int64(0), int64(0), int64(0), nil, nil}
}

// rgGroups answers the group list: the two system groups and two user groups
// on reports, the second of which is what the pool-scoped tests look for.
func rgGroups() fakeResponse {
	return fakeResponse{match: rgGroupList, cols: 14, rows: [][]driver.Value{
		rgGroup(2, "default", 2, "default"),
		rgGroup(1, "internal", 1, "internal"),
		rgGroup(257, "adhoc", 256, "reports"),
		rgGroup(258, "nightly", 256, "reports"),
	}}
}

// newFakeConnEdition is newFakeConn for an engine edition and version.
func newFakeConnEdition(t *testing.T, edition int64, version string, responses ...fakeResponse) *db.ServerConn {
	t.Helper()
	info := serverInfoResponse()
	info.rows[0][2] = version
	info.rows[0][8] = edition
	sc, _ := newFakeConnFrom(t, append([]fakeResponse{info, sysInfoResponse()}, responses...))
	return sc
}

func TestResourceGovernorSupportedByEdition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		info    *gosmo.ServerInfo
		want    bool
		comment string
	}{
		{"no info", nil, true, "fail open"},
		{"Enterprise 13", &gosmo.ServerInfo{EngineEdition: 3, VersionMajor: 13}, true, ""},
		{"Standard 16", &gosmo.ServerInfo{EngineEdition: 2, VersionMajor: 16}, false, "before 2025"},
		{"Standard 17", &gosmo.ServerInfo{EngineEdition: 2, VersionMajor: 17}, true, "2025 Standard gained it"},
		{"Express 17", &gosmo.ServerInfo{EngineEdition: 4, VersionMajor: 17}, false, ""},
		{"Managed Instance", &gosmo.ServerInfo{EngineEdition: 8, VersionMajor: 12}, true, "D5: shown, unverified"},
		{"Azure SQL Database", &gosmo.ServerInfo{EngineEdition: 5, VersionMajor: 12}, false, ""},
	} {
		if got := resourceGovernorSupported(tc.info); got != tc.want {
			t.Errorf("%s: supported = %v, want %v %s", tc.name, got, tc.want, tc.comment)
		}
	}
}

func TestManagementListsResourceGovernorExceptOnAzureSQLDatabase(t *testing.T) {
	ctx := context.Background()
	has := func(sc *db.ServerConn) bool {
		children, err := loadManagementChildren(loaderCtx{ctx: ctx, sc: sc}, &explorerNode{})
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(children, func(n *explorerNode) bool { return n.data.Type == NodeResourceGovernor })
	}
	if !has(newFakeConnEdition(t, 3, "16.0.4085.2", rgConfig(true), rgStatus(false))) {
		t.Error("Developer: Management has no Resource Governor node")
	}
	if !has(newFakeConnEdition(t, 8, "12.0.2000.8")) {
		t.Error("Managed Instance: Management has no Resource Governor node — D5 ships it shown")
	}
	if has(newFakeConnEdition(t, 5, "12.0.2000.8")) {
		t.Error("Azure SQL Database: Management lists Resource Governor, which the platform owns")
	}
	// Standard before 2025 keeps the node: its expansion says why it is empty.
	if !has(newFakeConnEdition(t, 2, "16.0.4085.2")) {
		t.Error("Standard 2022: Management has no Resource Governor node to explain the edition on")
	}
}

func TestResourceGovernorLabelCarriesItsState(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		responses   []fakeResponse
		want        string
		wantEnabled bool
	}{
		{"enabled, nothing pending", []fakeResponse{rgConfig(true), rgStatus(false)}, "Resource Governor", true},
		{"enabled, pending", []fakeResponse{rgConfig(true), rgStatus(true)}, "Resource Governor (Reconfiguration pending)", true},
		// Pending while disabled means only "changed since DISABLE" — W1.
		{"disabled, pending", []fakeResponse{rgConfig(false), rgStatus(true)}, "Resource Governor (Disabled)", false},
		{"enabled, status unreadable", []fakeResponse{rgConfig(true),
			{match: rgStatusRead, err: errors.New("Msg 300, VIEW SERVER STATE permission was denied")}}, "Resource Governor", true},
		// No VIEW ANY DEFINITION: zero rows, no error — gosmo's not-found.
		{"catalog not visible", []fakeResponse{{match: rgConfigRead, cols: 5}}, "Resource Governor", true},
	} {
		sc := newFakeConnEdition(t, 3, "16.0.4085.2", tc.responses...)
		n := resourceGovernorNode(loaderCtx{ctx: ctx, sc: sc})
		if n.label != tc.want || n.data.IsEnabled != tc.wantEnabled {
			t.Errorf("%s: label %q enabled %v, want %q enabled %v", tc.name, n.label, n.data.IsEnabled, tc.want, tc.wantEnabled)
		}
	}

	// An edition without Resource Governor is not asked at all: its label
	// stays bare and nothing is read.
	info := serverInfoResponse()
	info.rows[0][8] = int64(4)
	sc, inst := newFakeConnFrom(t, []fakeResponse{info, sysInfoResponse()})
	before := inst.QueryCount()
	if n := resourceGovernorNode(loaderCtx{ctx: ctx, sc: sc}); n.label != "Resource Governor" {
		t.Errorf("Express: label %q", n.label)
	}
	if inst.QueryCount() != before {
		t.Errorf("Express: the state label read %v", inst.Reads(""))
	}
}

func TestResourceGovernorOnAnUnsupportedEditionSaysSo(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 2, "16.0.4085.2")
	children, err := loadResourceGovernorChildren(loaderCtx{ctx: ctx, sc: sc}, &explorerNode{})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].data.Type != NodeError || children[0].label != resourceGovernorUnsupportedLabel {
		t.Errorf("Standard 2022 expands to %v, want the single unsupported row", labelsOfNodes(children))
	}

	sc = newFakeConnEdition(t, 2, "17.0.1000.7")
	children, _ = loadResourceGovernorChildren(loaderCtx{ctx: ctx, sc: sc}, &explorerNode{})
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"Resource Pools", "External Resource Pools"}) {
		t.Errorf("Standard 2025 expands to %v", got)
	}
}

func TestResourcePoolsFolderListsPoolsAndMarksTheSystemOnes(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 3, "16.0.4085.2", rgPools()...)
	children, err := loadResourcePoolsChildren(loaderCtx{ctx: ctx, sc: sc}, &explorerNode{})
	if err != nil {
		t.Fatal(err)
	}
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"default (system)", "internal (system)", "reports"}) {
		t.Fatalf("pools = %v", got)
	}
	reports := children[2]
	if reports.data.Name != "reports" || reports.data.IsSystem || reports.data.Type != NodeResourcePool {
		t.Errorf("reports node = %+v", reports.data)
	}
	if !children[1].data.IsSystem || children[1].data.Name != "internal" {
		t.Errorf("internal node = %+v, want IsSystem and the bare name", children[1].data)
	}

	sub, _ := loadResourcePoolChildren(loaderCtx{ctx: ctx, sc: sc}, reports)
	if len(sub) != 1 || sub[0].data.Type != NodeWorkloadGroups || sub[0].data.RGPool != "reports" {
		t.Errorf("a pool's children = %v, want one Workload Groups folder carrying the pool", labelsOfNodes(sub))
	}
}

func TestEmptyResourceGovernorCatalogIsNotVisibleNotEmpty(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 3, "16.0.4085.2",
		fakeResponse{match: rgPoolList, cols: 9},
		fakeResponse{match: rgGroupList, cols: 14},
		fakeResponse{match: "FROM   sys.resource_governor_external_resource_pools\nORDER", cols: 6})
	l := loaderCtx{ctx: ctx, sc: sc}
	for name, load := range map[string]childLoader{
		"Resource Pools":          loadResourcePoolsChildren,
		"Workload Groups":         loadWorkloadGroupsChildren,
		"External Resource Pools": loadExternalResourcePoolsChildren,
	} {
		children, err := load(l, &explorerNode{data: nodeData{RGPool: "default"}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(children) != 1 || children[0].data.Type != NodeError {
			t.Errorf("%s with an empty catalog = %v, want the one not-visible row", name, labelsOfNodes(children))
		}
	}
	if _, _, err := resourcePoolsFolderDetail(ctx, sc, &explorerNode{}, new([]nodeData)); !errors.Is(err, errResourceGovernorNotVisible) {
		t.Errorf("pools Details with an empty catalog: err %v, want errResourceGovernorNotVisible", err)
	}
}

func TestWorkloadGroupsFolderListsOnlyItsPool(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 3, "16.0.4085.2", rgGroups())
	l := loaderCtx{ctx: ctx, sc: sc}
	folder := &explorerNode{data: nodeData{Type: NodeWorkloadGroups, RGPool: "reports"}}
	children, err := loadWorkloadGroupsChildren(l, folder)
	if err != nil {
		t.Fatal(err)
	}
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"adhoc", "nightly"}) {
		t.Errorf("reports' groups = %v", got)
	}
	folder.data.RGPool = "default"
	children, _ = loadWorkloadGroupsChildren(l, folder)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"default (system)"}) {
		t.Errorf("default's groups = %v", got)
	}
	// A user pool no group uses is empty, not "not visible".
	folder.data.RGPool = "idle"
	children, _ = loadWorkloadGroupsChildren(l, folder)
	if len(children) != 0 {
		t.Errorf("an unused pool's groups = %v, want none", labelsOfNodes(children))
	}
}

// rgStats answers both DMVs: reports has 3 active + 1 queued across its two
// groups, and 2 memory-grant waiters.
func rgStats() []fakeResponse {
	start := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	pool := func(id int64, name string, cpu, used, waiters int64) []driver.Value {
		return []driver.Value{id, name, start, cpu, int64(0), used, int64(0), int64(0),
			int64(0), int64(0), waiters, int64(0), int64(0), int64(0)}
	}
	group := func(id int64, name string, poolID, active, queued int64) []driver.Value {
		return []driver.Value{id, name, poolID, start, int64(10), int64(0), active, queued,
			int64(500), int64(0), int64(0), int64(0), int64(0), int64(0)}
	}
	return []fakeResponse{
		{match: rgPoolStatsRead, cols: 14, rows: [][]driver.Value{
			pool(1, "internal", 9, 100, 0), pool(2, "default", 7, 200, 0), pool(256, "reports", 1234, 4096, 2),
		}},
		{match: rgGrpStatsRead, cols: 14, rows: [][]driver.Value{
			group(1, "internal", 1, 0, 0), group(2, "default", 2, 5, 0),
			group(257, "adhoc", 256, 1, 1), group(258, "nightly", 256, 2, 0),
		}},
	}
}

// cell returns the named column of the row whose first cell is name.
func cell(t *testing.T, cols []string, rows [][]string, name, col string) string {
	t.Helper()
	c := slices.Index(cols, col)
	if c < 0 {
		t.Fatalf("no column %q in %v", col, cols)
	}
	for _, r := range rows {
		if r[0] == name {
			return r[c]
		}
	}
	t.Fatalf("no row %q", name)
	return ""
}

func TestResourcePoolsDetailShowsLiveLoadPerPool(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 3, "16.0.4085.2", append(append(rgPools(), rgStats()...), rgGroups())...)
	cols, rows, err := resourcePoolsFolderDetail(ctx, sc, &explorerNode{data: nodeData{Type: NodeResourcePools}}, new([]nodeData))
	if err != nil {
		t.Fatal(err)
	}
	// reports is not the first row, and its requests are the sum of two
	// groups — a pane reading the first pool's or one group's would differ.
	for col, want := range map[string]string{
		"Max CPU %": "40", "Max mem %": "30", "Active": "3", "Queued": "1",
		"Grant waits": "2", "CPU ms": "1234", "Used KB": "4096",
	} {
		if got := cell(t, cols, rows, "reports", col); got != want {
			t.Errorf("reports %s = %q, want %q", col, got, want)
		}
	}
	if got := cell(t, cols, rows, "default (system)", "Active"); got != "5" {
		t.Errorf("default Active = %q, want 5", got)
	}
}

func TestResourcePoolsDetailWithoutViewServerStateLeavesLiveColumnsBlank(t *testing.T) {
	ctx := context.Background()
	denied := errors.New("Msg 300, VIEW SERVER STATE permission was denied")
	sc := newFakeConnEdition(t, 3, "16.0.4085.2", append(rgPools(),
		fakeResponse{match: rgPoolStatsRead, err: denied},
		fakeResponse{match: rgGrpStatsRead, err: denied})...)
	cols, rows, err := resourcePoolsFolderDetail(ctx, sc, &explorerNode{data: nodeData{Type: NodeResourcePools}}, new([]nodeData))
	if err != nil {
		t.Fatalf("the view failed instead of degrading: %v", err)
	}
	if got := cell(t, cols, rows, "reports", "Max CPU %"); got != "40" {
		t.Errorf("reports Max CPU %% = %q — the configuration should still show", got)
	}
	for _, col := range []string{"Active", "Queued", "Grant waits", "CPU ms", "Used KB"} {
		if got := cell(t, cols, rows, "reports", col); got != "" {
			t.Errorf("reports %s = %q without VIEW SERVER STATE, want blank", col, got)
		}
	}
}

func TestResourceGovernorDetailSeparatesStoredFromInForce(t *testing.T) {
	ctx := context.Background()
	sc := newFakeConnEdition(t, 3, "16.0.4085.2",
		fakeResponse{match: rgConfigRead, cols: 5, rows: [][]driver.Value{{true, int64(1205579333), "dbo", "fn_classify", int64(0)}}},
		fakeResponse{match: rgStatusRead, cols: 5, rows: [][]driver.Value{{int64(0), nil, nil, true, int64(40)}}},
		rgStats()[0])
	_, rows, err := resourceGovernorDetail(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0]] = r[1]
	}
	for k, want := range map[string]string{
		"Enabled": "Yes", "Classifier function": "dbo.fn_classify", "Classifier in force": "(none)",
		"Reconfiguration pending": "Yes", "Max outstanding I/O per volume": "Default",
		"I/O per volume in force": "40", "Statistics since": "2026-09-30 08:00:00",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestResourceGovernorDetailDegradesEachHalfAlone(t *testing.T) {
	ctx := context.Background()
	rowsOf := func(sc *db.ServerConn) map[string]string {
		t.Helper()
		_, rows, err := resourceGovernorDetail(ctx, sc)
		if err != nil {
			t.Fatalf("the view failed instead of degrading: %v", err)
		}
		got := map[string]string{}
		for _, r := range rows {
			got[r[0]] = r[1]
		}
		return got
	}
	// VIEW SERVER STATE without VIEW ANY DEFINITION: the stored half is
	// invisible (zero rows), what is in force still shows.
	got := rowsOf(newFakeConnEdition(t, 3, "16.0.4085.2",
		fakeResponse{match: rgConfigRead, cols: 5}, rgStatus(true)))
	if got["Configuration"] == "" || got["Reconfiguration pending"] != "Yes" {
		t.Errorf("stored invisible: rows %v", got)
	}
	// The reverse: stored config, no DMV.
	got = rowsOf(newFakeConnEdition(t, 3, "16.0.4085.2", rgConfig(true),
		fakeResponse{match: rgStatusRead, err: errors.New("Msg 300")}))
	if got["Enabled"] != "Yes" || got["In force"] == "" {
		t.Errorf("status invisible: rows %v", got)
	}
	// Pending while disabled is qualified, not a bare Yes.
	got = rowsOf(newFakeConnEdition(t, 3, "16.0.4085.2", rgConfig(false), rgStatus(true)))
	if got["Reconfiguration pending"] != "Yes — applied when enabled" {
		t.Errorf("disabled + pending = %q", got["Reconfiguration pending"])
	}
}
