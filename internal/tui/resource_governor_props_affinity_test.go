package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The affinity half of the pool pages (N6): Automatic and the scheduler/CPU
// grid, through the fake driver.

const rgSchedulersRead = "FROM   sys.dm_os_schedulers s"

// rgSchedulers answers Server.Schedulers: four schedulers on CPUs 0-3 in
// processor group 0, two NUMA nodes, scheduler 3 offline.
func rgSchedulers() fakeResponse {
	return fakeResponse{match: rgSchedulersRead, cols: 5, rows: [][]driver.Value{
		{int64(0), int64(0), int64(0), int64(0), true},
		{int64(1), int64(1), int64(0), int64(0), true},
		{int64(2), int64(2), int64(1), int64(0), true},
		{int64(3), int64(3), int64(1), int64(0), false},
	}}
}

// rgPoolAffinityRows answers the pool affinity read with rows of
// (pool_id, processor_group, scheduler_mask).
func rgPoolAffinityRows(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: rgPoolAffinity, cols: 3, rows: rows}
}

func rgPoolPage(t *testing.T, focus string, first ...fakeResponse) (*propsheet.Form, propApply, *fakeInstance, func() error) {
	t.Helper()
	sc, inst := newFakeConn(t, append(first, rgReads(rgConfig(true))...)...)
	f, apply := loadPage(t, pageRGPools(sc, &rgModel{}, focus), inst)
	return f, apply, inst, func() error { return runRGApply(context.Background(), sc, apply) }
}

func TestRGPoolAffinityWrites(t *testing.T) {
	// reports (256) is pinned to schedulers 1 and 3.
	pools, _, inst, apply := rgPoolPage(t, "", rgPoolAffinityRows([]driver.Value{int64(256), int64(0), int64(0b1010)}))
	grid := plainGrid(t, pools)
	aff := toggleGrid(t, pools)

	selectGridRow(t, grid, 0, "reports")
	if checkRow(t, pools, "Automatic scheduler affinity").Checked() {
		t.Fatal("a pinned pool shows Automatic")
	}
	if got := aff.Values(); !slices.EqualFunc(got, [][]bool{{false}, {true}, {false}, {true}}, slices.Equal) {
		t.Fatalf("reports' schedulers ticked %v, want 1 and 3", got)
	}
	if got := aff.Text()[3][0]; got != "3 (offline)" {
		t.Errorf("offline scheduler listed as %q", got)
	}
	// Unpin it.
	editCheck(t, pools, "Automatic scheduler affinity", true)

	// default: tick scheduler 2 — ticking turns Automatic off by itself.
	selectGridRow(t, grid, 0, "default (system)")
	toggleByName(t, aff, "2", 0)
	toggleByName(t, aff, "0", 0)
	if checkRow(t, pools, "Automatic scheduler affinity").Checked() {
		t.Error("ticking a scheduler left Automatic checked")
	}

	// A new pool, pinned to CPU node 1's schedulers.
	editText(t, pools, "New resource pool", "etl")
	clickButton(t, pools, "Add")
	toggleByName(t, aff, "2", 0)
	toggleByName(t, aff, "3 (offline)", 0)

	if err := apply(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE RESOURCE POOL [etl] WITH (AFFINITY SCHEDULER = (2 TO 3))",
		"ALTER RESOURCE POOL [default] WITH (AFFINITY SCHEDULER = (0, 2))",
		"ALTER RESOURCE POOL [reports] WITH (AFFINITY SCHEDULER = AUTO)",
		"ALTER RESOURCE GOVERNOR RECONFIGURE",
	}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("statements:\n  got  %q\n  want %q", got, want)
	}
}

// The grid column follows an edit once the selection moves on, as the
// limits do.
func TestRGPoolAffinityColumn(t *testing.T) {
	pools, _, _, _ := rgPoolPage(t, "reports")
	editCheck(t, pools, "Automatic scheduler affinity", false)
	toggleByName(t, toggleGrid(t, pools), "1", 0)
	toggleByName(t, toggleGrid(t, pools), "2", 0)
	grid := plainGrid(t, pools)
	selectGridRow(t, grid, 0, "default (system)")
	for i := 0; grid.Row(i) != nil; i++ {
		if r := grid.Row(i); r[0] == "reports" {
			if got := r[len(r)-1]; got != "1-2" {
				t.Errorf("reports' Affinity cell = %q, want 1-2", got)
			}
			return
		}
	}
	t.Fatal("reports is not listed")
}

// Automatic off with nothing ticked has no statement to write: refused on
// Apply, nothing sent.
func TestRGPoolAffinityNoneTickedIsRefused(t *testing.T) {
	pools, _, inst, apply := rgPoolPage(t, "reports")
	editCheck(t, pools, "Automatic scheduler affinity", false)
	err := apply()
	if err == nil || !strings.Contains(err.Error(), "tick at least one scheduler for resource pool reports") {
		t.Fatalf("apply = %v, want the none-ticked refusal", err)
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("statements sent anyway: %q", got)
	}
}

// A pool pinned where the grid cannot show it — another processor group, or
// a scheduler the instance no longer lists — is shown and not edited, and
// internal accepts no ALTER at all.
func TestRGPoolAffinityNotEditable(t *testing.T) {
	for _, tc := range []struct {
		name, pool, cell string
		aff              fakeResponse
	}{
		{"processor group 1", "reports", "group 1: 0x1", rgPoolAffinityRows([]driver.Value{int64(256), int64(1), int64(1)})},
		{"unlisted scheduler", "reports", "group 0: 0x100", rgPoolAffinityRows([]driver.Value{int64(256), int64(0), int64(0x100)})},
		{"internal", "internal", "Auto", rgPoolAffinityRows()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools, _, _, _ := rgPoolPage(t, tc.pool, tc.aff)
			if !checkRow(t, pools, "Automatic scheduler affinity").ReadOnly() || !toggleGrid(t, pools).ReadOnly() {
				t.Error("affinity is editable")
			}
			grid := plainGrid(t, pools)
			row := grid.Row(grid.SelectedRow())
			if got := row[len(row)-1]; got != tc.cell {
				t.Errorf("Affinity cell = %q, want %q", got, tc.cell)
			}
		})
	}
	// Another pool on the same page still is.
	pools, _, _, _ := rgPoolPage(t, "default", rgPoolAffinityRows([]driver.Value{int64(256), int64(1), int64(1)}))
	if toggleGrid(t, pools).ReadOnly() {
		t.Error("default's affinity is read-only because reports' is")
	}
}

// Without VIEW SERVER STATE the schedulers cannot be listed: affinity is
// shown, not edited, with a note, and the limits still are.
func TestRGPoolAffinityWithoutSchedulers(t *testing.T) {
	pools, _, inst, apply := rgPoolPage(t, "reports",
		fakeResponse{match: rgSchedulersRead, err: errors.New("VIEW SERVER STATE permission was denied")})
	if !toggleGrid(t, pools).ReadOnly() || !checkRow(t, pools, "Automatic scheduler affinity").ReadOnly() {
		t.Error("affinity editable with no scheduler list")
	}
	if !slices.ContainsFunc(pools.Rows(), func(r propsheet.Row) bool {
		n, ok := r.(*propsheet.NoteRow)
		return ok && strings.Contains(n.Text(), "listing the schedulers requires VIEW SERVER STATE")
	}) {
		t.Error("no note says why affinity cannot be edited")
	}
	editText(t, pools, "Maximum CPU %", "60")
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	if got := inst.Statements(); len(got) != 2 || got[0] != "ALTER RESOURCE POOL [reports] WITH (MAX_CPU_PERCENT = 60)" {
		t.Errorf("statements = %q", got)
	}
}

// External pools take CPU ids.
func TestRGExternalPoolCPUAffinity(t *testing.T) {
	sc, inst := newFakeConn(t, rgReads(rgConfig(true))...)
	ext, apply := loadPage(t, pageRGExternalPools(sc, &rgModel{}, "default"), inst)
	if got := toggleGrid(t, ext).Text()[0][0]; got != "0" {
		t.Errorf("first CPU listed as %q", got)
	}
	toggleByName(t, toggleGrid(t, ext), "2", 0)
	if err := runRGApply(context.Background(), sc, apply); err != nil {
		t.Fatal(err)
	}
	want := []string{"ALTER EXTERNAL RESOURCE POOL [default] WITH (AFFINITY CPU = (2))", "ALTER RESOURCE GOVERNOR RECONFIGURE"}
	if got := inst.Statements(); !slices.Equal(got, want) {
		t.Errorf("statements:\n  got  %q\n  want %q", got, want)
	}
}

// Revert puts every pool's affinity back.
func TestRGPoolAffinityRevert(t *testing.T) {
	pools, _, inst, apply := rgPoolPage(t, "reports")
	toggleByName(t, toggleGrid(t, pools), "1", 0)
	selectGridRow(t, plainGrid(t, pools), 0, "default (system)")
	if !pools.Dirty() {
		t.Fatal("an affinity edit left the page clean")
	}
	pools.Revert()
	if pools.Dirty() {
		t.Error("the page is dirty after Revert")
	}
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	if got := inst.Statements(); !slices.Equal(got, []string{"ALTER RESOURCE GOVERNOR RECONFIGURE"}) {
		t.Errorf("statements after Revert = %q", got)
	}
}

func TestRGAffinityText(t *testing.T) {
	for _, tc := range []struct {
		a    rgAffinity
		want string
	}{
		{rgAffinity{auto: true}, "Auto"},
		{rgAffinity{ids: []int{}}, "None"},
		{rgAffinity{ids: []int{0, 1, 2, 3, 6}}, "0-3, 6"},
		{rgAffinity{ids: []int{5}}, "5"},
	} {
		if got := tc.a.text(); got != tc.want {
			t.Errorf("%+v.text() = %q, want %q", tc.a, got, tc.want)
		}
	}
	if a, ok := rgAffinityFromMasks([]int{0}, []int64{-1 << 63}); !ok || !slices.Equal(a.ids, []int{63}) {
		t.Errorf("bit 63 read as %+v, %v", a, ok)
	}
	if a, ok := rgAffinityFromMasks(nil, nil); !ok || !a.auto {
		t.Errorf("no mask read as %+v, %v", a, ok)
	}
}
