package tui

import (
	"context"
	"slices"
	"testing"
)

// The permission gate for the seven Service Broker families, probed live
// 2026-09-16 with a WITHOUT
// LOGIN user per right on majors 13, 14 and 17, which answered identically.
//
// Six of the seven have no schema, which is the case docs/db-rules.md's rule
// is about: such a node falling to objectWriteRights() is asked about
// ALTER ANY SCHEMA — which permits none of these drops — and never about the
// narrow right that does.

// serviceBrokerNarrowRights is each schemaless family's own right, as the
// server spells it. A broker priority is absent on purpose: SQL Server
// enforces a BROKER PRIORITY permission it does not publish, so there is none
// to ask for.
var serviceBrokerNarrowRights = map[NodeType]string{
	NodeMessageType:          "ALTER ANY MESSAGE TYPE",
	NodeContract:             "ALTER ANY CONTRACT",
	NodeBrokerService:        "ALTER ANY SERVICE",
	NodeRoute:                "ALTER ANY ROUTE",
	NodeRemoteServiceBinding: "ALTER ANY REMOTE SERVICE BINDING",
}

// TestTheNarrowServiceBrokerRightPermitsTheDrop. The narrow right alone, with
// the database-wide ALTER and CONTROL both answering 0, keeps Delete — the
// shape a WITHOUT LOGIN user granted only that right had when it ran each
// DROP live.
func TestTheNarrowServiceBrokerRightPermitsTheDrop(t *testing.T) {
	for nodeType, right := range serviceBrokerNarrowRights {
		sc := probedConn(t, "appdb", nil, nil, []string{right},
			[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"})
		rights := objectOpRights(nodeType)
		if !allowsActionOn(sc, "appdb", "", "obj", rights...) {
			t.Errorf("%v: Delete withheld from a principal holding %s", nodeType, right)
		}
		// gateOn shows only rights[0] in a withheld item's note, so the
		// narrowest sufficient right has to lead: "needs CONTROL" sends a user
		// who wants to drop a route away to ask for the database.
		if got := rights[0].name; got != right {
			t.Errorf("%v: the withheld item's note names %q, want the narrow %q", nodeType, got, right)
		}
	}
}

// TestOneServiceBrokerRightPermitsNothingInAnotherFamily. ALTER ANY MESSAGE
// TYPE cannot alter a route (Msg 15151), so a set that shared one entry across
// the families would offer every drop to a principal holding any one right.
func TestOneServiceBrokerRightPermitsNothingInAnotherFamily(t *testing.T) {
	for nodeType, right := range serviceBrokerNarrowRights {
		var others []string
		for _, name := range serviceBrokerNarrowRights {
			if name != right {
				others = append(others, name)
			}
		}
		sc := probedConn(t, "appdb", nil, nil, others,
			[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA", right})
		if allowsActionOn(sc, "appdb", "", "obj", objectOpRights(nodeType)...) {
			t.Errorf("%v: Delete offered to a principal holding every right but %s", nodeType, right)
		}
	}
}

// TestABrokerPriorityIsGatedOnTheDatabasesAlter. There is no
// ALTER ANY BROKER PRIORITY — HAS_PERMS_BY_NAME answers NULL for every
// spelling, which reads as CapabilityUnknown and gates nothing — so the set is
// ALTER on the database and the right that subsumes it, and naming a
// broker-priority permission would withhold nothing forever.
func TestABrokerPriorityIsGatedOnTheDatabasesAlter(t *testing.T) {
	rights := objectOpRights(NodeBrokerPriority)
	for _, r := range rights {
		if r.name != "ALTER" && r.name != "CONTROL" {
			t.Errorf("a broker priority is gated on %q, which the server does not publish", r.name)
		}
	}
	sc := probedConn(t, "appdb", nil, nil, []string{"ALTER"}, []string{"CONTROL", "ALTER ANY SCHEMA"})
	if !allowsActionOn(sc, "appdb", "", "obj", rights...) {
		t.Error("Delete withheld from a principal holding ALTER on the database")
	}
	// Every other Service Broker right held, and the priority still withheld:
	// none of them confers a BROKER PRIORITY permission.
	var others []string
	for _, name := range serviceBrokerNarrowRights {
		others = append(others, name)
	}
	sc = probedConn(t, "appdb", nil, nil, others, []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"})
	if allowsActionOn(sc, "appdb", "", "obj", rights...) {
		t.Error("Delete offered to a principal holding only the other families' rights")
	}
}

// TestNoSchemalessServiceBrokerFamilyAsksAboutSchemas. ALTER ANY SCHEMA
// permits no schemaless drop, and a family that fell to objectWriteRights()
// would ask about it and about nothing narrower — the failure
// TestSchemalessDatabaseOpsAreGated exists for, one family earlier.
func TestNoSchemalessServiceBrokerFamilyAsksAboutSchemas(t *testing.T) {
	schemaless := []NodeType{
		NodeMessageType, NodeContract, NodeBrokerService, NodeRoute,
		NodeRemoteServiceBinding, NodeBrokerPriority,
	}
	for _, nodeType := range schemaless {
		if _, ok := dbScopedOpRights[nodeType]; !ok {
			t.Errorf("%v has no schema and no dbScopedOpRights entry; it falls to "+
				"objectWriteRights(), which asks ALTER ANY SCHEMA and not the right that permits it", nodeType)
			continue
		}
		rights := objectOpRights(nodeType)
		if slices.ContainsFunc(rights, func(r requiredRight) bool { return r.name == rightAlterAnySchema.name }) {
			t.Errorf("%v's rights name ALTER ANY SCHEMA, which permits no schemaless drop", nodeType)
		}
		if slices.ContainsFunc(rights, func(r requiredRight) bool { return r.object || r.schema }) {
			t.Errorf("%v is schemaless but carries an object- or schema-scoped right, "+
				"which is asked about nothing and answers for nobody", nodeType)
		}
	}
}

// TestAQueuesAlterAndDropTakeDifferentRights is the one asymmetry the probe
// found: ALTER ON OBJECT::<queue> writes the Properties page and is refused
// the drop (Msg 15151), which needs CONTROL on the queue or ALTER on its
// schema. One entry shared between the two verbs is wrong whichever way it is
// written.
func TestAQueuesAlterAndDropTakeDifferentRights(t *testing.T) {
	// A principal granted ALTER on the queue itself and nothing else: every
	// database- and schema-scope permission reads 0 for such a login. The
	// schema has to be answered, not merely unmentioned — an unprobed schema
	// reads unknown and fails open, which is right at run time and would make
	// this test pass for the wrong reason.
	sc, _ := newFakeConn(t, capabilityResponsesWithObjects(true,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, []string{"dbo"}, []string{"dbo.ClaimQueue"}, nil)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")

	if !allowsActionOn(sc, "appdb", "dbo", "ClaimQueue", queueAlterRights()...) {
		t.Error("Queue Properties comes up read-only for a principal holding ALTER on the queue")
	}
	if allowsActionOn(sc, "appdb", "dbo", "ClaimQueue", queueDropRights()...) {
		t.Error("Delete is offered to a principal holding only ALTER on the queue, which the server refuses")
	}
	if allowsActionOn(sc, "appdb", "dbo", "ClaimQueue", objectOpRights(NodeBrokerQueue)...) {
		t.Error("objectOpRights answers the queue's Delete with its Properties set")
	}
}

// TestAQueuesDropFollowsItsSchema. ALTER on the queue's schema permits the
// drop, and is the right a principal without CONTROL on the queue actually
// holds.
func TestAQueuesDropFollowsItsSchema(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponsesWithSchemas(true, nil, nil, nil,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, []string{"dbo"}, nil)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")

	if !allowsActionOn(sc, "appdb", "dbo", "ClaimQueue", queueDropRights()...) {
		t.Error("Delete withheld from a principal holding ALTER on the queue's schema")
	}
}

// TestTheQueuePageIsGatedOnTheQueueItself. An object-scoped right asked
// without a securable answers for nobody, so the page a principal granted
// ALTER on one queue can write would come up read-only — the failure
// withRequiresOn exists for, and one that compiles and looks right.
func TestTheQueuePageIsGatedOnTheQueueItself(t *testing.T) {
	sc, _ := newFakeConn(t)
	pages := brokerQueuePropPages(sc, "appdb", "dbo", "ClaimQueue")
	if len(pages) != 1 {
		t.Fatalf("want one page, got %d", len(pages))
	}
	p := pages[0]
	if p.requiresSchema != "dbo" || p.requiresObject != "ClaimQueue" {
		t.Errorf("the page names securable %q.%q, want dbo.ClaimQueue", p.requiresSchema, p.requiresObject)
	}
	if !slices.ContainsFunc(p.requires, func(r requiredRight) bool { return r.object }) {
		t.Error("the page declares no object-scoped right, so a grant on the queue itself speaks for nobody")
	}
}

// TestTheRoutePageAndTheRoutesDeleteShareOneRight. ALTER ANY ROUTE permits
// both, unlike a queue's two verbs — pinned so the two never drift apart
// silently.
func TestTheRoutePageAndTheRoutesDeleteShareOneRight(t *testing.T) {
	sc, _ := newFakeConn(t)
	pages := routePropPages(sc, "appdb", "ClaimRoute")
	names := func(rights []requiredRight) []string {
		var out []string
		for _, r := range rights {
			out = append(out, r.name)
		}
		return out
	}
	if got, want := names(pages[0].requires), names(objectOpRights(NodeRoute)); !slices.Equal(got, want) {
		t.Errorf("Route Properties requires %v, its Delete %v", got, want)
	}
	if pages[0].requiresIn != "appdb" {
		t.Errorf("the page reads its rights against %q, want the route's database", pages[0].requiresIn)
	}
}
