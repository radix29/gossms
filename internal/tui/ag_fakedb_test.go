package tui

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Shared fixtures for the three Availability Group Properties pages.
//
// Every page reads and writes through the group's *primary* replica, and
// agOnPrimary treats an unreachable primary as a hard error rather than
// degrading. IsLocalPrimary compares the group's primary_replica against the
// connected server's own name, so the fixture's primary replica has to be
// called what serverInfoResponse says this instance is called — hence a
// replica named FAKE\SQL alongside two ordinary ones. Without that the pages
// would try to open a peer connection, which the fake has no way to serve.
//
// Every AG write is a bare ALTER AVAILABILITY GROUP on the connection, with no
// USE in front of it, so they are read back with Statements().

const (
	agFixtureName = "AAG1"
	agPrimary     = `FAKE\SQL`
	agSecondary   = "ubusql2"
	agAsyncPeer   = "ubusql3"
)

var agEpoch = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

// agGroupResponse is the 18-column sys.availability_groups read. The column
// count is fixed across server versions — gosmo substitutes typed literals for
// columns an older instance lacks — so one shape serves every test.
func agGroupResponse() fakeResponse {
	return fakeResponse{match: "FROM sys.availability_groups ag", cols: 18, rows: [][]driver.Value{{
		"ag-0001", agFixtureName, "res-1", "resg-1",
		"WSFC", "SECONDARY",
		int64(3), int64(30000), int64(1),
		false, false, false, false,
		int64(0), false,
		agPrimary, "ONLINE", "HEALTHY",
	}}}
}

// agReplicaRow is one row of the 23-column replica read.
func agReplicaRow(id, name, availability, failover, secondaryRole, seeding, routingURL string, timeout, priority int64, role string) []driver.Value {
	return []driver.Value{
		"ag-0001", id, name, "TCP://" + name + ":5022",
		availability, failover, timeout,
		"ALL", secondaryRole,
		priority, routingURL, seeding,
		agEpoch, agEpoch,
		name == agPrimary, role, "ONLINE", "CONNECTED", "ONLINE", "HEALTHY",
		int64(0), "", nil,
	}
}

// agReplicasResponse scripts three replicas whose settings all differ, so no
// one replica's value can stand in for another's and the one a test acts on is
// never the first.
func agReplicasResponse() fakeResponse {
	return fakeResponse{match: "FROM sys.availability_replicas ar", cols: 23, rows: [][]driver.Value{
		agReplicaRow("rep-1", agPrimary, "SYNCHRONOUS_COMMIT", "AUTOMATIC", "NO", "AUTOMATIC", "", 10, 50, "PRIMARY"),
		agReplicaRow("rep-2", agSecondary, "SYNCHRONOUS_COMMIT", "AUTOMATIC", "READ_ONLY", "AUTOMATIC", "TCP://ubusql2:1433", 10, 50, "SECONDARY"),
		agReplicaRow("rep-3", agAsyncPeer, "ASYNCHRONOUS_COMMIT", "MANUAL", "NO", "MANUAL", "", 20, 30, "SECONDARY"),
	}}
}

// agDatabasesResponse feeds the General page's read-only database grid — one
// database, one row per replica, the shape the cross join produces.
func agDatabasesResponse() fakeResponse {
	row := func(replicaID, replica, state string, primary bool) []driver.Value {
		return []driver.Value{
			"ag-0001", replicaID, replica, "appdb", "agdb-1",
			replica == agPrimary, primary,
			state, "HEALTHY", "ONLINE",
			false, "",
			int64(0), int64(0), int64(0), int64(0), int64(0),
			agEpoch, agEpoch, agEpoch, agEpoch, agEpoch,
		}
	}
	return fakeResponse{match: "FROM sys.availability_databases_cluster adc", cols: 22, rows: [][]driver.Value{
		row("rep-1", agPrimary, "SYNCHRONIZED", true),
		row("rep-2", agSecondary, "SYNCHRONIZED", false),
		row("rep-3", agAsyncPeer, "SYNCHRONIZING", false),
	}}
}

// agRoutingListResponses answer the per-replica routing-list read. Scoped by
// the replica id the query is parameterised with: without that every replica
// would report the primary's list, and the page would come up claiming all
// three route somewhere.
func agRoutingListResponses() []fakeResponse {
	return []fakeResponse{
		{match: "availability_read_only_routing_lists", arg: "rep-1", cols: 2, rows: [][]driver.Value{
			{int64(1), agSecondary},
			{int64(2), agAsyncPeer},
		}},
		{match: "availability_read_only_routing_lists", cols: 2, rows: nil},
	}
}

func agResponses() []fakeResponse {
	responses := []fakeResponse{agGroupResponse(), agReplicasResponse(), agDatabasesResponse()}
	return append(responses, agRoutingListResponses()...)
}

// loadAGPage opens one of the three pages over the shared fixture and insists
// the replica grid actually filled — a page whose reads were half-scripted
// would otherwise present an empty grid and a passing "nothing was written".
func loadAGPage(t *testing.T, page func(sc *db.ServerConn) propPage) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	t.Helper()
	sc, inst := newFakeConn(t, agResponses()...)
	form, apply := loadPage(t, page(sc), inst)
	grid := agReplicaGrid(t, form)
	if grid.Row(2) == nil {
		t.Fatal("the replica grid has fewer than three rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form, grid
}

// agReplicaGrid returns the replica grid. The General page has two grids — the
// read-only database list comes first — so plainGrid cannot be used there, and
// the replica grid is the one with the cell cursor.
func agReplicaGrid(t *testing.T, f *propsheet.Form) *controls.DataGrid {
	t.Helper()
	var found *controls.DataGrid
	for _, r := range f.Rows() {
		gr, ok := r.(*propsheet.GridRow)
		if !ok || !gr.Grid.CellCursorEnabled() {
			continue
		}
		if found != nil {
			t.Fatal("this page has more than one cell-cursor grid; find it by hand")
		}
		found = gr.Grid
	}
	if found == nil {
		t.Fatal("no replica grid on this page")
	}
	return found
}

const agReplicaNameCol = 0
