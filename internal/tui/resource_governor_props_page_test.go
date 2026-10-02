package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Resource Governor Properties, driven through the fake driver. Statement
// text is gosmo's to test; these pin what the dialog asks for and in what
// order — the plan's phases across pages, and the RECONFIGURE or DISABLE every
// Apply ends in.

const (
	rgExternalList     = "FROM   sys.resource_governor_external_resource_pools\nORDER  BY name"
	rgExternalAffinity = "sys.resource_governor_external_resource_pool_affinity"
	rgCandidatesRead   = "master.sys.sql_modules"
)

// rgExternals answers the external pool list: default only, as every
// instance in the estate has it (W1).
func rgExternals() []fakeResponse {
	return []fakeResponse{
		{match: rgExternalList, cols: 6, rows: [][]driver.Value{
			{int64(2), "default", int64(100), int64(20), int64(0), int64(1)},
		}},
		{match: rgExternalAffinity, cols: 3},
	}
}

// rgCandidates answers the classifier list, every function in dbo.
func rgCandidates(names ...string) fakeResponse {
	r := fakeResponse{match: rgCandidatesRead, cols: 2}
	for _, n := range names {
		r.rows = append(r.rows, []driver.Value{"dbo", n})
	}
	return r
}

// rgConfigWith answers the stored configuration with a classifier.
func rgConfigWith(enabled bool, schema, name string, io int64) fakeResponse {
	return fakeResponse{match: rgConfigRead, cols: 5, rows: [][]driver.Value{{enabled, int64(901), schema, name, io}}}
}

// rgReads is every read the four pages make, config first so a test can put
// its own ahead of it.
func rgReads(config fakeResponse) []fakeResponse {
	r := []fakeResponse{config, rgStatus(false), rgCandidates("rg_classify", "rg_other"), rgGroups()}
	r = append(r, rgPools()...)
	r = append(r, rgExternals()...)
	r = append(r, rgSchedulers())
	// CreateResourcePool reads its pool back; none found hands back the
	// name-only handle, which is all the dialog wants of it.
	return append(r, fakeResponse{match: "FROM   sys.resource_governor_resource_pools\nWHERE  name = @p1", cols: 9})
}

// runRGApply runs apply closures as the dialog's Apply does: planned, then
// finished by runResourceGovernorPlan.
func runRGApply(ctx context.Context, sc *db.ServerConn, applies ...propApply) error {
	return plannedApply(applies, func(ctx context.Context, plan *applyPlan) error {
		return runResourceGovernorPlan(ctx, sc, plan)
	})(ctx)
}

// rgSelectPool lists another pool on the Workload Groups page. The dropdown
// is a view control, so editSelect's dirty check does not apply.
func rgSelectPool(t *testing.T, f *propsheet.Form, pool string) {
	t.Helper()
	row := selectRow(t, f, "Resource pool")
	i := slices.Index(row.Items(), pool)
	if i < 0 {
		t.Fatalf("Resource pool offers %q, not %q", row.Items(), pool)
	}
	row.Edit(i)
	if row.Dirty() {
		t.Fatal("listing another pool made the page dirty")
	}
}

// rgMoveGroup moves the selected group to pool. The move is committed at
// once and the group leaves the grid, so it is the page, not the row, that
// is dirty afterwards.
func rgMoveGroup(t *testing.T, f *propsheet.Form, pool string) {
	t.Helper()
	row := selectRow(t, f, "Uses resource pool")
	i := slices.Index(row.Items(), pool)
	if i < 0 {
		t.Fatalf("Uses resource pool offers %q, not %q", row.Items(), pool)
	}
	row.Edit(i)
	if !f.Dirty() {
		t.Fatal("the page is not dirty after moving a group — apply will skip it")
	}
}

// TestRGApplyOrdersStatementsAcrossPages is the reason the dialog is planned.
// Applied page by page, the Resource Pools page's DROP of reports would run
// before the Workload Groups page emptied it (Msg 10916), and nothing would
// RECONFIGURE.
func TestRGApplyOrdersStatementsAcrossPages(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)

	pools, applyPools := loadPage(t, pageRGPools(sc, &rgModel{}, ""), inst)
	poolGrid := plainGrid(t, pools)
	selectGridRow(t, poolGrid, 0, "reports")
	clickButton(t, pools, "Remove")
	editText(t, pools, "New resource pool", "etl")
	clickButton(t, pools, "Add")
	// Add selects the new row; its limits are the detail rows now.
	editText(t, pools, "Maximum CPU %", "60")
	selectGridRow(t, poolGrid, 0, "default (system)")
	editText(t, pools, "Maximum memory %", "80")

	groups, applyGroups := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{}), inst)
	groupGrid := plainGrid(t, groups)
	rgSelectPool(t, groups, "reports")
	selectGridRow(t, groupGrid, 0, "nightly")
	rgMoveGroup(t, groups, "default")
	selectGridRow(t, groupGrid, 0, "adhoc")
	clickButton(t, groups, "Remove")
	rgSelectPool(t, groups, "default")
	editText(t, groups, "New workload group", "batch")
	clickButton(t, groups, "Add")

	if err := runRGApply(context.Background(), sc, applyPools, applyGroups); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE RESOURCE POOL [etl] WITH (MAX_CPU_PERCENT = 60)",
		"CREATE WORKLOAD GROUP [batch] USING [default]",
		"ALTER WORKLOAD GROUP [nightly] USING [default]",
		"DROP WORKLOAD GROUP [adhoc]",
		"DROP RESOURCE POOL [reports]",
		"ALTER RESOURCE POOL [default] WITH (MAX_MEMORY_PERCENT = 80)",
		"ALTER RESOURCE GOVERNOR RECONFIGURE",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("statements:\n  got  %q\n  want %q", got, want)
	}
}

// TestRGPoolPagesReachWorkloadGroupsBeforeApply (N9): a pool added on
// Resource Pools takes a new group in the same Apply, pools being created
// first; one removed there is no longer a destination, but stays listed while
// a group is in it, with a hint to move it out. Workload Groups loaded first
// hears of an edit; loaded after, it reads it.
func TestRGPoolPagesReachWorkloadGroupsBeforeApply(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	model := &rgModel{}
	groups, applyGroups := loadPage(t, pageRGGroups(sc, model, rgFocus{}), inst)
	pools, applyPools := loadPage(t, pageRGPools(sc, model, ""), inst)

	editText(t, pools, "New resource pool", "etl")
	clickButton(t, pools, "Add")
	rgSelectPool(t, groups, "etl")
	editText(t, groups, "New workload group", "batch")
	clickButton(t, groups, "Add")
	later, _ := loadPage(t, pageRGGroups(sc, model, rgFocus{}), inst)
	if got := selectRow(t, later, "Resource pool").Items(); !slices.Contains(got, "etl") {
		t.Errorf("Workload Groups loaded after the edit lists %q, without etl", got)
	}
	if err := runRGApply(context.Background(), sc, applyPools, applyGroups); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE RESOURCE POOL [etl]",
		"CREATE WORKLOAD GROUP [batch] USING [etl]",
		"ALTER RESOURCE GOVERNOR RECONFIGURE",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("statements:\n  got  %q\n  want %q", got, want)
	}

	sc, inst = newFakeConn(t, rgReads(rgConfig(true))...)
	model = &rgModel{}
	groups, _ = loadPage(t, pageRGGroups(sc, model, rgFocus{pool: "reports"}), inst)
	pools, _ = loadPage(t, pageRGPools(sc, model, ""), inst)
	selectGridRow(t, plainGrid(t, pools), 0, "reports")
	clickButton(t, pools, "Remove")
	if got := selectRow(t, groups, "Resource pool").Value(); got != "reports" {
		t.Errorf("Workload Groups lists %q, not reports, which still holds adhoc and nightly", got)
	}
	if got := hintText(t, groups); !strings.Contains(got, "move its groups") {
		t.Errorf("hint = %q, want one asking to move reports' groups", got)
	}
	rgSelectPool(t, groups, "default")
	if got := selectRow(t, groups, "Uses resource pool").Items(); slices.Contains(got, "reports") {
		t.Errorf("default's group may move to %q: reports, being removed, among them", got)
	}
	// Listed again after the removal, not during it: the hint still says so.
	rgSelectPool(t, groups, "reports")
	if got := hintText(t, groups); !strings.Contains(got, "move its groups") {
		t.Errorf("hint = %q on listing reports again, want one asking to move its groups", got)
	}
}

// TestRGApplyKeepsADisabledGovernorDisabled: RECONFIGURE enables the
// governor (W1), so an Apply on a disabled one ends in DISABLE instead.
func TestRGApplyKeepsADisabledGovernorDisabled(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(false))...)
	f, apply := loadPage(t, pageRGExternalPools(sc, &rgModel{}, ""), inst)
	editText(t, f, "Maximum memory %", "30")
	if err := runRGApply(context.Background(), sc, apply); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER EXTERNAL RESOURCE POOL [default] WITH (MAX_MEMORY_PERCENT = 30)",
		"ALTER RESOURCE GOVERNOR DISABLE",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("statements:\n  got  %q\n  want %q", got, want)
	}
}

// TestRGGeneralPage: the classifier and I/O go first, and the Enabled box
// decides the finish without a read of the governor's state.
func TestRGGeneralPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		final   string
	}{
		{"enable", true, "ALTER RESOURCE GOVERNOR RECONFIGURE"},
		{"disable", false, "ALTER RESOURCE GOVERNOR DISABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, rgReads(rgConfigWith(!tc.enabled, "dbo", "rg_classify", 40))...)
			f, apply := loadPage(t, pageRGGeneral(&PropDialog{}, sc), inst)
			if got := selectRow(t, f, "Classifier function").Value(); got != "dbo.rg_classify" {
				t.Fatalf("classifier shows %q, want the stored dbo.rg_classify", got)
			}
			editCheck(t, f, "Enabled", tc.enabled)
			editSelect(t, f, "Classifier function", rgNoClassifier)
			editText(t, f, "Max outstanding I/O per volume", "0")
			reads := len(inst.Reads(rgConfigRead))
			if err := runRGApply(context.Background(), sc, apply); err != nil {
				t.Fatal(err)
			}
			want := []string{
				"ALTER RESOURCE GOVERNOR WITH (CLASSIFIER_FUNCTION = NULL)",
				"ALTER RESOURCE GOVERNOR WITH (MAX_OUTSTANDING_IO_PER_VOLUME = DEFAULT)",
				tc.final,
			}
			if got := inst.Statements(); !slices.Equal(got, want) {
				t.Errorf("statements:\n  got  %q\n  want %q", got, want)
			}
			if n := len(inst.Reads(rgConfigRead)); n != reads {
				t.Errorf("the finish re-read the governor (%d reads, was %d) though Enabled said what to do", n, reads)
			}
		})
	}
}

// TestRGClassifierChoices: a stored classifier the candidate list lacks is
// still shown as itself, never as another function or as none.
func TestRGClassifierChoices(t *testing.T) {
	cands := []gosmo.ClassifierFunction{{Schema: "dbo", Name: "a"}, {Schema: "dbo", Name: "b"}}
	for _, tc := range []struct {
		name, schema, fn string
		id               int
		want             string
	}{
		{"none", "", "", 0, rgNoClassifier},
		{"listed", "dbo", "b", 7, "dbo.b"},
		{"no longer qualifies", "rg", "old", 7, "rg.old"},
		{"not visible", "", "", 7, "object_id 7 (not visible)"},
	} {
		choices, i := rgClassifierChoices(cands, tc.schema, tc.fn, tc.id)
		if got := rgClassifierItem(choices[i]); got != tc.want {
			t.Errorf("%s: shows %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRGPagesPreselectTheLeafTheyOpenedFrom: a leaf's Properties opens on its
// own row. Neither focus is the first row, so a page ignoring it fails.
func TestRGPagesPreselectTheLeafTheyOpenedFrom(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	pools, _ := loadPage(t, pageRGPools(sc, &rgModel{}, "reports"), inst)
	if got := staticValue(t, pools, "Name"); got != "reports" {
		t.Errorf("Resource Pools opened on %q, want reports", got)
	}
	if got := textRow(t, pools, "Maximum CPU %").Value(); got != "40" {
		t.Errorf("detail shows Maximum CPU %% %s, want reports' 40", got)
	}
	groups, _ := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{group: "nightly"}), inst)
	if got := selectRow(t, groups, "Resource pool").Value(); got != "reports" {
		t.Errorf("Workload Groups lists pool %q, want nightly's pool reports", got)
	}
	if got := staticValue(t, groups, "Name"); got != "nightly" {
		t.Errorf("Workload Groups opened on %q, want nightly", got)
	}
	groups, _ = loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{pool: "reports"}), inst)
	if got := selectRow(t, groups, "Resource pool").Value(); got != "reports" {
		t.Errorf("Workload Groups for pool reports lists %q", got)
	}
}

// TestRGInternalIsReadOnly: internal accepts no ALTER (Msg 10915), so its
// limits are not offered for editing, and nothing is written for it.
func TestRGInternalIsReadOnly(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	pools, _ := loadPage(t, pageRGPools(sc, &rgModel{}, "internal"), inst)
	if !textRow(t, pools, "Maximum CPU %").ReadOnly() {
		t.Error("internal's limits are editable")
	}
	groups, _ := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{group: "internal"}), inst)
	if !textRow(t, groups, "Maximum DOP").ReadOnly() || !selectRow(t, groups, "Importance").ReadOnly() {
		t.Error("the internal group's settings are editable")
	}
	selectGridRow(t, plainGrid(t, groups), 0, "internal (system)")
	clickButton(t, groups, "Remove")
	if groups.Dirty() {
		t.Error("removing the internal group was accepted")
	}
}

// TestRGFractionalGrantNeeds2019: a fractional REQUEST_MAX_MEMORY_GRANT_PERCENT
// is a syntax error before 2019; the row refuses it there rather than the
// server.
func TestRGFractionalGrantNeeds2019(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{{"13.0.6500.1", false}, {"15.0.4415.2", true}} {
		sc, inst := newFakeConnAtVersion(t, tc.version, rgReads(rgConfig(true))...)
		f, _ := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{group: "default"}), inst)
		editText(t, f, "Max memory grant %", "12.5")
		if err := textRow(t, f, "Max memory grant %").Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: 12.5 validates = %v, want %v (%v)", tc.version, err == nil, tc.ok, err)
		}
	}
}

// TestRGTempdbLimitsOnlyFrom2025 and the NULL that clears one.
func TestRGTempdbLimitsOnlyFrom2025(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	f, _ := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{}), inst)
	for _, r := range f.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && strings.HasPrefix(tr.Label(), "Tempdb") {
			t.Errorf("major 16 offers %q, a 2025 option", tr.Label())
		}
	}

	groupsWithLimit := rgGroups()
	groupsWithLimit.rows[2][12] = float64(10) // adhoc: 10 % of tempdb
	sc, inst = newFakeConnAtVersion(t, "17.0.1135.8", append([]fakeResponse{groupsWithLimit}, rgReads(rgConfig(true))...)...)
	f, apply := loadPage(t, pageRGGroups(sc, &rgModel{}, rgFocus{group: "adhoc"}), inst)
	if got := textRow(t, f, "Tempdb data limit %").Value(); got != "10" {
		t.Fatalf("adhoc's tempdb limit shows %q, want 10", got)
	}
	editText(t, f, "Tempdb data limit %", "")
	editText(t, f, "Tempdb data limit (MB)", "512")
	if err := runRGApply(context.Background(), sc, apply); err != nil {
		t.Fatal(err)
	}
	want := "ALTER WORKLOAD GROUP [adhoc] WITH (GROUP_MAX_TEMPDB_DATA_PERCENT = NULL, GROUP_MAX_TEMPDB_DATA_MB = 512)"
	if got := inst.Statements(); len(got) == 0 || got[0] != want {
		t.Errorf("statements %q, want %q first", got, want)
	}
}

// TestRGScriptChangesWritesNothing: Script Changes runs the same plan under
// WithScript — the statements in order, the finish included, nothing sent.
func TestRGScriptChangesWritesNothing(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	f, apply := loadPage(t, pageRGPools(sc, &rgModel{}, ""), inst)
	editText(t, f, "New resource pool", "etl")
	clickButton(t, f, "Add")
	ctx, script := gosmo.WithScript(context.Background())
	if err := runRGApply(ctx, sc, apply); err != nil {
		t.Fatal(err)
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("Script Changes executed %q", got)
	}
	want := []string{"CREATE RESOURCE POOL [etl]", "ALTER RESOURCE GOVERNOR RECONFIGURE"}
	if got := script.Statements(); !slices.Equal(got, want) {
		t.Errorf("script %q, want %q", got, want)
	}
}

// TestRGFailureAfterAWriteReloadsEverything: once a statement has landed the
// plan's failure is a committed one, which reloads every page — the plan
// interleaved them, so no page's edits can be kept as unsent.
func TestRGFailureAfterAWriteReloadsEverything(t *testing.T) {
	refused := errors.New("Msg 10916")
	responses := append([]fakeResponse{{match: "DROP RESOURCE POOL", err: refused}}, rgReads(rgConfig(true))...)
	sc, inst := newFakeConn(t, responses...)
	f, apply := loadPage(t, pageRGPools(sc, &rgModel{}, ""), inst)
	selectGridRow(t, plainGrid(t, f), 0, "reports")
	clickButton(t, f, "Remove")
	editText(t, f, "New resource pool", "etl")
	clickButton(t, f, "Add")
	err := runRGApply(context.Background(), sc, apply)
	if _, ok := errors.AsType[committedApplyError](err); !ok {
		t.Errorf("err = %v, want a committed failure (CREATE landed before the DROP failed)", err)
	}
	if got := inst.Statements(); slices.Contains(got, "ALTER RESOURCE GOVERNOR RECONFIGURE") {
		t.Error("RECONFIGURE ran after a failed step")
	}
}

// TestPlannedPageRefusesToApplyAlone: a planned page's apply outside a plan
// would write nothing in order; it says so instead.
func TestPlannedPageRefusesToApplyAlone(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	f, apply := loadPage(t, pageRGPools(sc, &rgModel{}, ""), inst)
	editText(t, f, "New resource pool", "etl")
	clickButton(t, f, "Add")
	if err := apply(context.Background()); !errors.Is(err, errNotPlanned) {
		t.Errorf("err = %v, want errNotPlanned", err)
	}
}

// TestRGNewClassifierClosesTheDialog: the dialog is modal, so the template
// is only usable once it is gone — New classifier... closes it before opening
// the query, and asks first when an edit would be lost with it (W7: the first
// version left it open, in front of a window no key could reach).
func TestRGNewClassifierClosesTheDialog(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t, rgReads(rgConfigWith(false, "", "", 0))...)
	d := NewPropDialog(a)
	a.propDialog = d
	pages := func() []propPage { return []propPage{pageRGGeneral(d, sc)} }
	open := func() *propsheet.Form {
		t.Helper()
		d.show(sc, "", "Resource Governor Properties", "", "", pages)
		// Not waitAndDrain: it returns after the first callback, which under
		// -race in the full suite was not always General's load.
		drainUntil(t, a, func() bool { return d.PageState(0) == propsheet.PageReady }, "General to load")
		return d.PageForm(0)
	}

	open()
	queries := a.queryPanelCnt
	rgNewClassifier(d, sc)
	if d.Visible() {
		t.Fatal("the dialog stayed open in front of the template")
	}
	if a.queryPanelCnt != queries+1 {
		t.Fatalf("query windows opened: %d, want 1", a.queryPanelCnt-queries)
	}

	f := open()
	editCheck(t, f, "Enabled", true)
	queries = a.queryPanelCnt
	rgNewClassifier(d, sc)
	if !a.confirmDialog.Visible() {
		t.Fatal("an unsaved edit was dropped without asking")
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if !d.Visible() || !f.Dirty() || a.queryPanelCnt != queries {
		t.Fatalf("No: dialog visible %v, edit kept %v, windows opened %d; want true, true, 0",
			d.Visible(), f.Dirty(), a.queryPanelCnt-queries)
	}
	rgNewClassifier(d, sc)
	answerConfirm(t, a, false)
	if d.Visible() || a.queryPanelCnt != queries+1 {
		t.Fatalf("Yes: dialog visible %v, windows opened %d; want false, 1", d.Visible(), a.queryPanelCnt-queries)
	}
}
