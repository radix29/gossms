package tui

import (
	"context"
	"slices"
	"testing"
)

// The Detail Browser's view of the seven Service Broker families.
//
// Three things are pinned here. A folder's rows must carry the objs mapping,
// without which the pane's Delete is withheld silently. A leaf must reach its
// own arm rather than fetchNodeDetails' default, which answers any unhandled
// type with a Name/Type/Database/Schema grid that never errors and says
// nothing. And the Queues folder's two DMV reads must not be able to cost the
// listing — a login without VIEW DATABASE STATE gets the queues either way.

// brokerFolderCases is one case per Service Broker folder: the responses its
// listing needs, and the row the fixtures make it show.
func brokerFolderCases() []struct {
	folder NodeType
	leaf   NodeType
	resp   []fakeResponse
	rows   int
} {
	return []struct {
		folder NodeType
		leaf   NodeType
		resp   []fakeResponse
		rows   int
	}{
		{NodeMessageTypes, NodeMessageType, []fakeResponse{messageTypeResp()}, 3},
		{NodeContracts, NodeContract, []fakeResponse{contractResp(), contractMessageResp()}, 2},
		{NodeBrokerQueues, NodeBrokerQueue, []fakeResponse{queueResp(), queueCountResp()}, 3},
		{NodeBrokerServices, NodeBrokerService, []fakeResponse{brokerServiceResp(), serviceContractResp()}, 2},
		{NodeRoutes, NodeRoute, []fakeResponse{routeResp()}, 2},
		{NodeRemoteServiceBindings, NodeRemoteServiceBinding, []fakeResponse{remoteServiceBindingResp()}, 1},
		{NodeBrokerPriorities, NodeBrokerPriority, []fakeResponse{brokerPriorityResp()}, 1},
	}
}

func TestEveryServiceBrokerFolderDetailCarriesItsObjects(t *testing.T) {
	for _, c := range brokerFolderCases() {
		sc, _ := newTypePropConn(t, c.resp...)
		var objs []nodeData
		node := &explorerNode{data: nodeData{Type: c.folder, DBName: brokerDB, conn: sc}}
		cols, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
		if err != nil {
			t.Errorf("%v: %v", c.folder, err)
			continue
		}
		if isFallback(cols, rows) {
			t.Errorf("%v falls to fetchNodeDetails' default arm", c.folder)
			continue
		}
		if len(rows) != c.rows {
			t.Errorf("%v listed %d rows, want %d", c.folder, len(rows), c.rows)
		}
		if len(objs) != len(rows) {
			t.Errorf("%v mapped %d row objects for %d rows — the pane's Delete is withheld",
				c.folder, len(objs), len(rows))
		}
		for _, o := range objs {
			if o.Type != c.leaf {
				t.Errorf("%v mapped a row to %v, want %v", c.folder, o.Type, c.leaf)
				break
			}
			if o.DBName != brokerDB {
				t.Errorf("%v mapped a row with no database — every verb on it needs one", c.folder)
				break
			}
		}
	}
}

// TestEveryServiceBrokerLeafHasItsOwnDetailView. The default arm never errors
// and never queries, so a leaf missed in the dispatch looks like a working
// pane that happens to say very little.
func TestEveryServiceBrokerLeafHasItsOwnDetailView(t *testing.T) {
	cases := []struct {
		typ    NodeType
		schema string
		name   string
		resp   []fakeResponse
	}{
		{NodeMessageType, "", "//claims/Submit",
			[]fakeResponse{brokerRowByArg(messageTypeResp(), "//claims/Submit", 1)}},
		{NodeContract, "", "//claims/Contract",
			[]fakeResponse{brokerRowByArg(contractResp(), "//claims/Contract", 1), contractMessageResp()}},
		{NodeBrokerQueue, "dbo", "ClaimQueue",
			[]fakeResponse{brokerRowByArg(queueResp(), "ClaimQueue", 0), oneQueueCountResp(), queueMonitorResp()}},
		{NodeBrokerService, "", "//claims/Service",
			[]fakeResponse{brokerRowByArg(brokerServiceResp(), "//claims/Service", 1), serviceContractResp()}},
		{NodeRoute, "", "ClaimRoute", []fakeResponse{brokerRowByArg(routeResp(), "ClaimRoute", 1)}},
		{NodeRemoteServiceBinding, "", "ClaimBinding",
			[]fakeResponse{brokerRowByArg(remoteServiceBindingResp(), "ClaimBinding", 0)}},
		{NodeBrokerPriority, "", "ClaimPriority",
			[]fakeResponse{brokerRowByArg(brokerPriorityResp(), "ClaimPriority", 0)}},
	}
	for _, c := range cases {
		sc, _ := newTypePropConn(t, c.resp...)
		cols, rows, objs := detailOf(t, sc, c.typ, c.schema, c.name)
		if isFallback(cols, rows) {
			t.Errorf("%v falls to fetchNodeDetails' default arm — it has no detail view of its own", c.typ)
			continue
		}
		if len(rows) == 0 {
			t.Errorf("%v's detail view is empty", c.typ)
		}
		// A leaf is one object, not a listing: objs is what offers Delete on
		// a row, and a Property/Value view has no rows to delete.
		if objs != nil {
			t.Errorf("%v's leaf view mapped %d row objects, want none", c.typ, len(objs))
		}
	}
}

// TestQueuesFolderShowsMessageCountsAndSurvivesWithoutThem. The count comes
// from sys.dm_db_partition_stats and needs VIEW DATABASE STATE, which the
// queue listing itself does not — so the column is blank, never 0, for a login
// that cannot read it, and the folder still lists.
func TestQueuesFolderShowsMessageCountsAndSurvivesWithoutThem(t *testing.T) {
	col := 5 // Messages

	sc, _ := newTypePropConn(t, queueResp(), queueCountResp())
	var objs []nodeData
	node := &explorerNode{data: nodeData{Type: NodeBrokerQueues, DBName: brokerDB, conn: sc}}
	cols, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("with counts: %v", err)
	}
	if cols[col] != "Messages" {
		t.Fatalf("column %d is %q, want Messages", col, cols[col])
	}
	if got := rows[0][col]; got != "42" {
		t.Errorf("ClaimQueue's message count reads %q, want 42", got)
	}
	// A queue with no statistics row is a queue nothing is known about, not
	// an empty one.
	if got := rows[1][col]; got != "" {
		t.Errorf("a queue with no statistics row reads %q, want blank", got)
	}

	// Without the DMV the folder must still list, with the column blank.
	sc, _ = newTypePropConn(t, queueResp())
	objs = nil
	node = &explorerNode{data: nodeData{Type: NodeBrokerQueues, DBName: brokerDB, conn: sc}}
	_, rows, err = fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("the folder failed when the message counts could not be read: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("listed %d queues without the DMV, want 3", len(rows))
	}
	for i, r := range rows {
		if r[col] != "" {
			t.Errorf("row %d reads %q for an unreadable count, want blank", i, r[col])
		}
	}
}

// TestQueueLeafSurvivesUnreadableDMVs. The per-queue count and the monitor row
// are both VIEW DATABASE STATE reads, and a queue the broker has never
// monitored has no monitor row at all — the normal case. Neither may cost the
// rows the catalog answered for.
func TestQueueLeafSurvivesUnreadableDMVs(t *testing.T) {
	sc, _ := newTypePropConn(t, brokerRowByArg(queueResp(), "ClaimQueue", 0))
	cols, rows, _ := detailOf(t, sc, NodeBrokerQueue, "dbo", "ClaimQueue")
	if isFallback(cols, rows) {
		t.Fatal("the queue leaf fell to the default arm")
	}
	if !slices.ContainsFunc(rows, func(r []string) bool { return r[0] == "Activation procedure" }) {
		t.Error("the queue's own rows are missing although the catalog answered")
	}
	for _, r := range rows {
		if r[0] == "Messages" || r[0] == "Monitor state" {
			t.Errorf("row %q was built from a DMV that could not be read", r[0])
		}
	}
}

// TestServiceBrokerFolderDetailAppliesTheFolderFilter. The pane queries gosmo
// independently of the tree, so a folder that skips filterObjects lists
// objects the tree has filtered away — and it must filter the collection, not
// the rows.
func TestServiceBrokerFolderDetailAppliesTheFolderFilter(t *testing.T) {
	sc, _ := newTypePropConn(t, messageTypeResp())
	var objs []nodeData
	node := &explorerNode{data: nodeData{Type: NodeMessageTypes, DBName: brokerDB,
		conn: sc, Filter: nameFilter("Submit")}}
	_, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(rows) != 1 || rows[0][0] != "//claims/Submit" {
		t.Errorf("pane listed %v, want only //claims/Submit", rows)
	}
	if len(objs) != len(rows) {
		t.Errorf("got %d row objects for %d rows", len(objs), len(rows))
	}
}

// TestEveryServiceBrokerLeafOffersProperties. A Properties page nothing opens
// is a page that does not exist, and the context menu is the only entry point
// these dialogs have.
func TestEveryServiceBrokerLeafOffersProperties(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")

	for _, typ := range []NodeType{
		NodeMessageType, NodeContract, NodeBrokerQueue, NodeBrokerService,
		NodeRoute, NodeRemoteServiceBinding, NodeBrokerPriority,
	} {
		node := &explorerNode{label: "x", data: nodeData{
			Type: typ, DBName: brokerDB, Schema: "dbo", Name: "x", conn: sc}}
		if !hasMenuItem(a.nodeMenuItems(node), "Properties...") {
			t.Errorf("%v offers no Properties item — its props pages are unreachable", typ)
		}
	}
}
