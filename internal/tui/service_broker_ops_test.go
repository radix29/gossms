package tui

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The object ops the seven Service Broker families offer once their reads,
// pages and gates are in place — Delete for
// all seven, Script as ▸ CREATE/DROP for all seven, Move to Schema for the
// queue alone, and no rename anywhere.

// serviceBrokerLeaves is every leaf node type in the subtree, with the schema
// and name its node carries. Only the queue has a schema.
var serviceBrokerLeaves = []struct {
	typ      NodeType
	schema   string
	name     string
	wantDrop string
}{
	{NodeMessageType, "", "//claims/Submit", `DROP MESSAGE TYPE [//claims/Submit]`},
	{NodeContract, "", "//claims/Contract", `DROP CONTRACT [//claims/Contract]`},
	{NodeBrokerQueue, "sales", "IdleQueue", `DROP QUEUE [sales].[IdleQueue]`},
	{NodeBrokerService, "", "//claims/Service", `DROP SERVICE [//claims/Service]`},
	{NodeRoute, "", "ClaimRoute", `DROP ROUTE [ClaimRoute]`},
	{NodeRemoteServiceBinding, "", "ClaimBinding", `DROP REMOTE SERVICE BINDING [ClaimBinding]`},
	{NodeBrokerPriority, "", "ClaimPriority", `DROP BROKER PRIORITY [ClaimPriority]`},
}

// TestEveryServiceBrokerLeafDrops drives Delete on each family and pins the
// statement. The names are the real ones: five of the seven are routinely
// named with characters no bare identifier allows ("//claims/Submit"), so a
// drop that forgot to quote would work on every fixture with a tidy name and
// fail on every real object.
func TestEveryServiceBrokerLeafDrops(t *testing.T) {
	for _, c := range serviceBrokerLeaves {
		t.Run(nodeTypeName(c.typ), func(t *testing.T) {
			a := newTestApp()
			sc, inst := opTestConn(t)
			node := opTestNode(sc, c.typ, c.schema, c.name, "")

			a.deleteObject(node)
			answerConfirm(t, a, false)
			waitAndDrain(t, a)

			stmts := inst.StatementsIn(brokerDB)
			if len(stmts) != 1 || stmts[0] != c.wantDrop {
				t.Errorf("statements = %q, want exactly %q", stmts, c.wantDrop)
			}
		})
	}
}

// TestNoServiceBrokerFamilyOffersARename. There is no sp_rename class for any
// of the seven, and ALTER SERVICE — the one ALTER that names a service —
// changes its queue and its contracts, not its name. A rename wired here would
// emit a statement the server cannot run.
func TestNoServiceBrokerFamilyOffersARename(t *testing.T) {
	for _, c := range serviceBrokerLeaves {
		op := objectOpFor(c.typ)
		if op == nil {
			t.Errorf("%v has no objectOps entry, so it offers no Delete either", c.typ)
			continue
		}
		if op.rename != nil {
			t.Errorf("%v offers a rename; no sp_rename path exists for it", c.typ)
		}
	}
}

// TestOnlyTheQueueMovesBetweenSchemas. Six of the seven have an owner and no
// schema at all, so Move to Schema on one of them would offer a transfer of an
// object ALTER SCHEMA cannot name.
func TestOnlyTheQueueMovesBetweenSchemas(t *testing.T) {
	for _, c := range serviceBrokerLeaves {
		op := objectOpFor(c.typ)
		if op == nil {
			continue
		}
		wantTransfer := c.typ == NodeBrokerQueue
		if got := op.transfer != nil; got != wantTransfer {
			t.Errorf("%v offers Move to Schema = %v, want %v", c.typ, got, wantTransfer)
		}
	}
}

// TestAQueuesMoveTakesControlNotAlter is the one right in this pass that is
// neither the page's nor the Delete's. Probed live 2026-09-16: CONTROL on the
// queue transfers it and ALTER on the queue is refused Msg 15151, so the
// object-scoped ALTER the other two sets ask about must not appear here — it
// reads 1 for a principal holding either.
func TestAQueuesMoveTakesControlNotAlter(t *testing.T) {
	n := nodeData{Type: NodeBrokerQueue, DBName: brokerDB, Schema: "sales", Name: "IdleQueue"}
	rights := objectTransferRights(n)
	for _, r := range rights {
		if sameRight(r, rightAlterOnObject) {
			t.Error("Move to Schema asks about ALTER on the queue, which the server refuses the transfer to")
		}
		if sameRight(r, rightAlterOnSchema) || sameRight(r, rightAlterAnySchema) || sameRight(r, rightAlterDatabase) {
			t.Errorf("Move to Schema asks about %q, which was refused the transfer live", r.name)
		}
	}
	if !slicesContainsRight(rights, rightControlOnObject) {
		t.Error("Move to Schema never asks about CONTROL on the queue, the right that permits it")
	}

	// A principal holding CONTROL on the queue and nothing else keeps the
	// item; the same principal holding only ALTER on it loses it.
	withControl := probedConnWithObject(t, brokerDB, "sales.IdleQueue", "CONTROL")
	if !allowsActionOn(withControl, brokerDB, "sales", "IdleQueue", rights...) {
		t.Error("Move to Schema withheld from a principal holding CONTROL on the queue")
	}
	withAlter := probedConnWithObject(t, brokerDB, "sales.IdleQueue", "ALTER")
	if allowsActionOn(withAlter, brokerDB, "sales", "IdleQueue", rights...) {
		t.Error("Move to Schema offered to a principal holding only ALTER on the queue")
	}
}

// TestEveryServiceBrokerLeafScriptsCreateAndDrop. No ALTER, even for the six
// families that have one — the choice the rest of the tree makes, and the
// queue's ALTER lives on its Properties page instead.
func TestEveryServiceBrokerLeafScriptsCreateAndDrop(t *testing.T) {
	want := []string{"CREATE To", "DROP To", "DROP And CREATE To"}
	for _, c := range serviceBrokerLeaves {
		s, ok := scriptables[c.typ]
		if !ok {
			t.Errorf("%v has no Script as entry", c.typ)
			continue
		}
		var got []string
		for _, v := range s.verbs {
			got = append(got, v.label)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%v scripts as %q, want %q", c.typ, got, want)
		}
		if s.noun != nodeTypeName(c.typ) {
			t.Errorf("%v is scripted as %q but named %q in the tree", c.typ, s.noun, nodeTypeName(c.typ))
		}
	}
}

// TestTheBindingsCreateIsWithheldOnAzure. CREATE REMOTE SERVICE BINDING is
// refused by a Managed Instance at *compile* time (Msg 41906), which aborts
// the whole batch before anything in it runs — so the two verbs that emit it
// are withheld rather than sent to be refused. Everything else the family
// offers still works there, and on-premises nothing is withheld at all.
func TestTheBindingsCreateIsWithheldOnAzure(t *testing.T) {
	for _, c := range []struct {
		name     string
		azure    bool
		disabled map[string]bool
	}{
		{"on Managed Instance", true, map[string]bool{"CREATE To": true, "DROP And CREATE To": true}},
		{"on premises", false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc := onPremConn(t)
			if c.azure {
				sc = azureConn(t)
			}
			node := opTestNode(sc, NodeRemoteServiceBinding, "", "ClaimBinding", "")
			node.data.conn = sc

			for _, verb := range scriptVerbItems(t, a, node) {
				gotDisabled := verb.Enabled != nil && !verb.Enabled()
				if gotDisabled != c.disabled[verb.Label] {
					t.Errorf("%q disabled = %v, want %v", verb.Label, gotDisabled, c.disabled[verb.Label])
				}
				if gotDisabled && verb.Note != "N/A" {
					t.Errorf("%q is withheld with Note %q, want the edition note", verb.Label, verb.Note)
				}
			}
		})
	}
}

// TestOnlyTheBindingIsEditionGated. Every other family's CREATE runs on a
// Managed Instance — probed live on t-qmi-01, all six created and dropped — so
// a gate reaching any of them would withhold something that works.
func TestOnlyTheBindingIsEditionGated(t *testing.T) {
	for _, c := range serviceBrokerLeaves {
		for _, label := range []string{"CREATE To", "DROP To", "DROP And CREATE To"} {
			want := c.typ == NodeRemoteServiceBinding && label != "DROP To"
			if got := editionRefusesScriptVerb(c.typ, label); got != want {
				t.Errorf("editionRefusesScriptVerb(%v, %q) = %v, want %v", c.typ, label, got, want)
			}
		}
	}
}

// TestAQueueDragsAsSchemaQualified. The queue is the one family with a schema,
// and a bare [IdleQueue] dropped into a query editor names a different object
// — or none — when the queue is not in dbo. The other six have no schema to
// qualify with.
func TestAQueueDragsAsSchemaQualified(t *testing.T) {
	sc, _ := opTestConn(t)
	if got := explorerDragText(opTestNode(sc, NodeBrokerQueue, "sales", "IdleQueue", "")); got != "[sales].[IdleQueue]" {
		t.Errorf("a queue drags as %q, want the two-part name", got)
	}
	if got := explorerDragText(opTestNode(sc, NodeRoute, "", "ClaimRoute", "")); got != "[ClaimRoute]" {
		t.Errorf("a route drags as %q, want the bare name", got)
	}
}

// scriptVerbItems is the second level of a node's "Script <Noun> as ▸"
// cascade, and fails when the node offers none.
func scriptVerbItems(t *testing.T, a *App, node *explorerNode) []controls.MenuItem {
	t.Helper()
	items := a.scriptMenuItems(node)
	if len(items) != 1 {
		t.Fatalf("scriptMenuItems returned %d items, want the one Script cascade", len(items))
	}
	return items[0].Sub
}

// probedConnWithObject is a login holding one object-scope permission on one
// object and nothing else. A CONTROL grant produces *both* rows, which is what
// the server answers: gosmo's object block matches CONTROL alongside whatever
// name it asks about, so an ALTER row exists for a CONTROL holder and not the
// other way round — the whole reason the two rights can be told apart at all.
func probedConnWithObject(t *testing.T, dbName, object, permission string) *db.ServerConn {
	t.Helper()
	// Every database- and schema-scope right is denied outright: an unprobed
	// one reads unknown and fails open, which would answer this test with
	// "offered" whatever the object map says.
	resp := capabilityResponsesWithObjects(true,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, []string{"sales"},
		[]string{object}, nil)
	if permission == "CONTROL" {
		for i, r := range resp {
			if r.match != "IS_ROLEMEMBER" {
				continue
			}
			r.rows = append(r.rows, []driver.Value{"O:CONTROL", object, int64(1)})
			resp[i] = r
		}
	}
	sc, _ := newFakeConn(t, resp...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), dbName)
	return sc
}

// sameRight compares two requiredRights, which are not comparable with == —
// one field is a slice. reflect.DeepEqual is what the rest of the gate tests
// use for the same reason.
func sameRight(a, b requiredRight) bool { return reflect.DeepEqual(a, b) }

// slicesContainsRight is slices.Contains for a requiredRight.
func slicesContainsRight(rights []requiredRight, want requiredRight) bool {
	for _, r := range rights {
		if sameRight(r, want) {
			return true
		}
	}
	return false
}

// TestScriptingEachFamilyReachesTheRightObject runs every family's CREATE
// generator against the catalog fixtures and reads the object back out of the
// script. What it pins is the binding rather than the text gosmo produces: a
// generator wired to the wrong Scripter method, or one handing a queue's name
// where its schema belongs, still compiles and still produces a plausible
// script — of the wrong object, or of none.
func TestScriptingEachFamilyReachesTheRightObject(t *testing.T) {
	for _, c := range []struct {
		typ    NodeType
		schema string
		name   string
		resp   []fakeResponse
		want   string
	}{
		{NodeMessageType, "", "//claims/Submit",
			[]fakeResponse{brokerRowByArg(messageTypeResp(), "//claims/Submit", 1)},
			"CREATE MESSAGE TYPE [//claims/Submit]"},
		{NodeContract, "", "//claims/Contract",
			[]fakeResponse{brokerRowByArg(contractResp(), "//claims/Contract", 1), contractMessageResp()},
			"CREATE CONTRACT [//claims/Contract]"},
		{NodeBrokerQueue, "sales", "IdleQueue",
			[]fakeResponse{brokerRowByArg(queueResp(), "IdleQueue", 2)},
			"CREATE QUEUE [sales].[IdleQueue]"},
		{NodeBrokerService, "", "//claims/Service",
			[]fakeResponse{brokerRowByArg(brokerServiceResp(), "//claims/Service", 1), serviceContractResp()},
			"CREATE SERVICE [//claims/Service]"},
		{NodeRoute, "", "ClaimRoute",
			[]fakeResponse{brokerRowByArg(routeResp(), "ClaimRoute", 1)},
			"CREATE ROUTE [ClaimRoute]"},
		{NodeRemoteServiceBinding, "", "ClaimBinding",
			[]fakeResponse{brokerRowByArg(remoteServiceBindingResp(), "ClaimBinding", 0)},
			"CREATE REMOTE SERVICE BINDING [ClaimBinding]"},
		{NodeBrokerPriority, "", "ClaimPriority",
			[]fakeResponse{brokerRowByArg(brokerPriorityResp(), "ClaimPriority", 0)},
			"CREATE BROKER PRIORITY [ClaimPriority]"},
	} {
		t.Run(nodeTypeName(c.typ), func(t *testing.T) {
			sc, _ := newFakeConn(t, append([]fakeResponse{brokerDBResp()}, c.resp...)...)
			n := nodeData{Type: c.typ, DBName: brokerDB, Schema: c.schema, Name: c.name}
			text, err := scriptables[c.typ].verbs[0].gen(context.Background(), sc, n)
			if err != nil {
				t.Fatalf("CREATE To: %v", err)
			}
			if !strings.Contains(text, c.want) {
				t.Errorf("the script does not name %s:\n%s", c.want, text)
			}
		})
	}
}

// TestAQueuesDeleteFollowsControlNotAlter is the other half of the queue's one
// asymmetry, and the case that was knowingly withheld until the object map
// could answer for CONTROL: probed live 2026-09-16 on major 17, a principal
// granted CONTROL on the queue and one owning it both dropped it, while ALTER
// on the queue is refused Msg 15151.
func TestAQueuesDeleteFollowsControlNotAlter(t *testing.T) {
	rights := objectOpRights(NodeBrokerQueue)
	if slicesContainsRight(rights, rightAlterOnObject) {
		t.Error("Delete asks about ALTER on the queue, which the server refuses the drop to")
	}

	withControl := probedConnWithObject(t, brokerDB, "sales.IdleQueue", "CONTROL")
	if !allowsActionOn(withControl, brokerDB, "sales", "IdleQueue", rights...) {
		t.Error("Delete withheld from a principal holding CONTROL on the queue")
	}
	withAlter := probedConnWithObject(t, brokerDB, "sales.IdleQueue", "ALTER")
	if allowsActionOn(withAlter, brokerDB, "sales", "IdleQueue", rights...) {
		t.Error("Delete offered to a principal holding only ALTER on the queue")
	}

	// The Properties page is the other way round: ALTER on the queue is
	// exactly what ALTER QUEUE takes.
	if !allowsActionOn(withAlter, brokerDB, "sales", "IdleQueue", queueAlterRights()...) {
		t.Error("Queue Properties withheld from a principal holding ALTER on the queue")
	}
}
