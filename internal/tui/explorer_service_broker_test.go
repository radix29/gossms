package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"
)

// The Object Explorer wiring for the seven Service Broker families. Same
// per-family checklist explorer_tree_families_test.go runs — the table-driven
// half lives there, with these types in its tables; what is here is the
// placement and the one loader-shaped assertion each family needs.
//
// Every assertion is about the tree. What each catalog read returns is gosmo's
// own live tests; the fake answers by substring and proves only that the
// loader asked and mapped the answer.

// The Service Broker folder sits between Query Store and Security, which is
// where SSMS puts it. A user navigating from SSMS has nothing else to go on.
func TestServiceBrokerFolderSitsWhereSSMSPutsIt(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	children, err := childLoaders[NodeDatabase](l,
		&explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	labels := labelsOfNodes(children)
	at := slices.Index(labels, "Service Broker")
	if at < 0 {
		t.Fatalf("a database's folders = %v, with no Service Broker", labels)
	}
	if got, want := at, slices.Index(labels, "Query Store")+1; got != want {
		t.Errorf("Service Broker is at %d, want %d — directly after Query Store", got, want)
	}
	if got, want := slices.Index(labels, "Security"), at+1; got != want {
		t.Errorf("Security is at %d, want %d — directly after Service Broker", got, want)
	}
	if children[at].data.Type != NodeServiceBroker || children[at].data.DBName != "appdb" {
		t.Errorf("the Service Broker folder = %+v", children[at].data)
	}
}

// Service Broker is a folder of folders, and each has to carry the database
// or its own read runs somewhere else.
func TestServiceBrokerFolderChildren(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	children := loadFamily(t, sc, NodeServiceBroker)

	want := []struct {
		label string
		typ   NodeType
	}{
		{"Message Types", NodeMessageTypes},
		{"Contracts", NodeContracts},
		{"Queues", NodeBrokerQueues},
		{"Services", NodeBrokerServices},
		{"Routes", NodeRoutes},
		{"Remote Service Bindings", NodeRemoteServiceBindings},
		{"Broker Priorities", NodeBrokerPriorities},
	}
	if len(children) != len(want) {
		t.Fatalf("Service Broker = %v, want %d folders", labelsOfNodes(children), len(want))
	}
	for i, w := range want {
		if children[i].label != w.label || children[i].data.Type != w.typ {
			t.Errorf("Service Broker[%d] = %q/%v, want %q/%v",
				i, children[i].label, children[i].data.Type, w.label, w.typ)
		}
		if children[i].data.DBName != "appdb" {
			t.Errorf("Service Broker[%d] lost the database name", i)
		}
	}
}

// The folder does not gate on is_broker_enabled: every one of these objects
// can be listed and dropped with the broker disabled — disabling it stops
// message delivery, not DDL — and SSMS shows the folder unconditionally.
// The database row the fake answers reports the flag off.
func TestServiceBrokerFolderIsListedWithTheBrokerDisabled(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	children, err := childLoaders[NodeDatabase](loaderCtx{ctx: context.Background(), sc: sc},
		&explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	if !slices.Contains(labelsOfNodes(children), "Service Broker") {
		t.Error("the Service Broker folder is missing — it must not gate on is_broker_enabled")
	}
}

// SQL Server's own message types list here as SSMS lists them, but marked
// IsSystem so Delete and Rename stay off their menu. The discriminator is the
// id range: system ids run 1..14, the first user object is 65536.
func TestMessageTypesLoaderMarksTheShippedOnesSystem(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.service_message_types mt", cols: 6,
		rows: [][]driver.Value{
			{int64(14), "DEFAULT", "", "BINARY", "", ""},
			{int64(65536), "//app/OrderRequest", "dbo", "XML", "dbo", "OrderSchema"},
		}})
	children := loadFamily(t, sc, NodeMessageTypes)
	want := []string{"DEFAULT", "//app/OrderRequest"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeMessageType)
	if !children[0].data.IsSystem {
		t.Error("a shipped message type is not marked IsSystem — Delete would be offered on it")
	}
	if children[1].data.IsSystem {
		t.Error("a user message type is marked IsSystem — Delete would be withheld from it")
	}
}

func TestContractsLoaderMarksTheShippedOnesSystem(t *testing.T) {
	sc := newFamilyConn(t,
		fakeResponse{
			match: "SELECT c.service_contract_id, c.name", cols: 3,
			rows: [][]driver.Value{
				{int64(6), "DEFAULT", ""},
				{int64(65536), "//app/OrderContract", "dbo"},
			}},
		fakeResponse{
			match: "sys.service_contract_message_usages", cols: 4,
			rows: [][]driver.Value{
				{int64(65536), "//app/OrderRequest", true, false},
			}})
	children := loadFamily(t, sc, NodeContracts)
	want := []string{"DEFAULT", "//app/OrderContract"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeContract)
	if !children[0].data.IsSystem || children[1].data.IsSystem {
		t.Error("IsSystem does not follow the id range — Delete would be offered on the wrong one")
	}
}

// Queues are the one schema-scoped family here, and the only one whose system
// members are told apart by is_ms_shipped rather than an id range: their
// object_ids are ordinary sys.objects ids, nowhere near 65536.
//
// A queue with STATUS = OFF is labelled "(Disabled)" — it neither receives nor
// enqueues anything and nothing else in the row says so.
func TestBrokerQueuesLoaderLabelsSchemaAndDisabledState(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.service_queues q", cols: 18,
		rows: [][]driver.Value{
			{int64(1977058079), "InternalMailQueue", "dbo", "", true, true, true, false, true,
				false, "", "", "", int64(0), "", "PRIMARY", treeFamilyCreated, treeFamilyCreated},
			{int64(1993058136), "OrderQueue", "sales", "", false, false, false, true, true,
				true, "[dbo].[usp_Orders]", "dbo", "usp_Orders", int64(4), "OWNER", "PRIMARY",
				treeFamilyCreated, treeFamilyCreated},
		}})
	children := loadFamily(t, sc, NodeBrokerQueues)
	want := []string{"dbo.InternalMailQueue", "sales.OrderQueue (Disabled)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	for _, n := range children {
		if n.data.DBName != "appdb" || n.data.Type != NodeBrokerQueue {
			t.Errorf("%q = %+v", n.label, n.data)
		}
	}
	// Schema and Name must be the queue's own, not sliced back out of the
	// label — everything downstream builds T-SQL from them, and Move to Schema
	// acts on the pair.
	if children[1].data.Schema != "sales" || children[1].data.Name != "OrderQueue" {
		t.Errorf("second child = %+v, want Schema=sales Name=OrderQueue", children[1].data)
	}
	if !children[0].data.IsSystem || children[1].data.IsSystem {
		t.Error("IsSystem does not follow is_ms_shipped — the id range is meaningless for queues")
	}
	if !children[0].data.IsEnabled || children[1].data.IsEnabled {
		t.Error("IsEnabled does not mirror the queue's STATUS")
	}
	if !children[0].data.CreateDate.Equal(treeFamilyCreated) {
		t.Errorf("CreateDate = %v, want %v", children[0].data.CreateDate, treeFamilyCreated)
	}
}

// The id range marks services, which is why msdb shows Database Mail's
// services as user objects while the same feature's queues come back system.
// The asymmetry is the catalog's; pinning it here is what keeps it from being
// rediscovered as a bug and "fixed" by name-matching.
func TestBrokerServicesLoaderMarksByIDRange(t *testing.T) {
	sc := newFamilyConn(t,
		fakeResponse{
			match: "SELECT s.service_id, s.name", cols: 5,
			rows: [][]driver.Value{
				{int64(3), "ServiceBroker", "", "dbo", "ServiceBrokerQueue"},
				{int64(65542), "InternalMailService", "dbo", "dbo", "InternalMailQueue"},
			}},
		fakeResponse{
			match: "sys.service_contract_usages", cols: 2,
			rows: [][]driver.Value{
				{int64(65542), "//app/OrderContract"},
			}})
	children := loadFamily(t, sc, NodeBrokerServices)
	want := []string{"ServiceBroker", "InternalMailService"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeBrokerService)
	if !children[0].data.IsSystem {
		t.Error("a service below 65536 is not marked IsSystem")
	}
	if children[1].data.IsSystem {
		t.Error("Database Mail's service is marked IsSystem — it sits in the user id range, " +
			"unlike the same feature's queue, and the two classifications cannot be made to agree")
	}
}

// No route is ever marked system: sys.routes has no is_ms_shipped and no
// system id range — AutoCreatedLocal, which SQL Server puts in every
// database, is route_id 65536, inside the user range. SSMS lists it plainly
// and a user may legitimately drop it.
func TestRoutesLoaderMarksNothingSystem(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.routes r", cols: 9,
		rows: [][]driver.Value{
			{int64(65536), "AutoCreatedLocal", "", "", "", "LOCAL", "", nil, nil},
			{int64(65537), "OrderRoute", "dbo", "//app/Orders", "", "TCP://svc:4022", "", nil, nil},
		}})
	children := loadFamily(t, sc, NodeRoutes)
	want := []string{"AutoCreatedLocal (LOCAL)", "OrderRoute (TCP://svc:4022)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeRoute)
	for _, n := range children {
		if n.data.IsSystem {
			t.Errorf("%q is marked IsSystem — no route ever is", n.label)
		}
	}
	// Name is the route's own, without the address suffix, which is
	// presentation — DROP ROUTE takes the name.
	if children[0].data.Name != "AutoCreatedLocal" {
		t.Errorf("Name = %q, want AutoCreatedLocal", children[0].data.Name)
	}
}

// The family exists on Managed Instance and is listed there:
// sys.remote_service_bindings reads fine and ALTER/DROP run. Only CREATE is
// refused (Msg 41906), which is the edition gate's business, not the tree's.
func TestRemoteServiceBindingsLoader(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.remote_service_bindings b", cols: 7,
		rows: [][]driver.Value{
			{int64(65536), "OrderBinding", "dbo", "//app/Orders", "RemoteUser", false, ""},
		}})
	children := loadFamily(t, sc, NodeRemoteServiceBindings)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"OrderBinding"}) {
		t.Fatalf("children = %v", got)
	}
	assertCarriesDatabase(t, children, NodeRemoteServiceBinding)
}

// The level is in the label because 1..10 is the one thing that says what a
// priority does, and the name never carries it.
func TestBrokerPrioritiesLoaderLabelsTheLevel(t *testing.T) {
	sc := newFamilyConn(t, fakeResponse{
		match: "FROM   sys.conversation_priorities p", cols: 6,
		rows: [][]driver.Value{
			{int64(65536), "HighOrders", "//app/OrderContract", "//app/Orders", "", int64(9)},
			{int64(65537), "LowBatch", "", "", "", int64(2)},
		}})
	children := loadFamily(t, sc, NodeBrokerPriorities)
	want := []string{"HighOrders (9)", "LowBatch (2)"}
	if got := labelsOfNodes(children); !slices.Equal(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	assertCarriesDatabase(t, children, NodeBrokerPriority)
	if children[0].data.Name != "HighOrders" {
		t.Errorf("Name = %q, want HighOrders — the label's suffix is presentation", children[0].data.Name)
	}
	// sys.conversation_priorities has no system members at all.
	for _, n := range children {
		if n.data.IsSystem {
			t.Errorf("%q is marked IsSystem — the view has no system members", n.label)
		}
	}
}

// Queues are the one schema-scoped family, so they are the only one of the
// seven whose folder offers a Schema criterion; offering it on the other six
// would match every row on an empty string.
func TestOnlyTheQueuesFolderOffersASchemaFilter(t *testing.T) {
	for _, nt := range []NodeType{NodeMessageTypes, NodeContracts, NodeBrokerServices,
		NodeRoutes, NodeRemoteServiceBindings, NodeBrokerPriorities} {
		for _, p := range filterProps(nt) {
			if p.id == fpSchema {
				t.Errorf("%v offers Schema, but the family has none — every row carries \"\"", nt)
			}
			if p.id == fpCreationDate {
				t.Errorf("%v offers Creation Date, but its catalog records none — every row would be rejected", nt)
			}
		}
	}
	var names []string
	for _, p := range filterProps(NodeBrokerQueues) {
		names = append(names, p.name)
	}
	if !slices.Equal(names, []string{"Name", "Schema"}) {
		t.Errorf("Queues filter properties = %v, want [Name Schema]", names)
	}
}
