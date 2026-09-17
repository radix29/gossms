package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// B5: the class-1 Move to Schema gate, against the table probed live on
// win10cli (major 17) on 2026-09-17, one WITHOUT LOGIN user per row and ALTER
// on the destination schema held throughout. Every row below is one of those
// users, as gosmo's probe reads it back, and what the server let it do with
// ALTER SCHEMA dst TRANSFER OBJECT::src.t.
//
//	right held                         O:ALTER O:CONTROL  transfer
//	ALTER on the object                      1         0  refused, Msg 15151
//	CONTROL on the object                    1         1  went through
//	CONTROL on the object, DENY ALTER        0         1  went through
//	CONTROL on the database                  1         1  went through
//	db_ddladmin                              1         0  refused, Msg 15151
//	CONTROL on the database, DENY CONTROL    0         0  refused, Msg 15151
//
// The third row is the one that is easy to get wrong and the reason Move to
// Schema is exempt from the ALTER-denial tests: a DENY of ALTER on the object
// does not reach a transfer, because the transfer asks CONTROL and CONTROL is
// what the principal still holds. Gating the move on the Rename/Delete set
// withheld it there and offered it on rows one and five.

// classOneTransferFamilies is every family whose Move to Schema reads the
// class-1 object map — the sys.objects families the tree offers a transfer on,
// the Service Broker queue among them. A type or an XML schema collection is
// not here: it is class 6 or class 10 and asks the per-securable probe, which
// permission_gate_securable_test.go covers.
var classOneTransferFamilies = []NodeType{
	NodeTable, NodeView, NodeStoredProcedure, NodeFunction,
	NodeSequence, NodeSynonym, NodeRule, NodeDefault, NodeBrokerQueue,
}

// classOneConn is a probed connection to appdb in which the principal holds
// the database-scope permissions in dbGranted and no other, ALTER on schema
// "sales" only if alterOnSchema, and the object-scope rows in objRows on
// sales.Thing. Every name the sets ask about is answered, as a real probe
// answers it: one left out reads unknown and fails open, and the case would
// pass for that reason rather than the one it names.
func classOneConn(t *testing.T, dbGranted []string, alterOnSchema bool, objRows map[string]int64) *db.ServerConn {
	t.Helper()
	var dbDenied []string
	for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"} {
		if !slices.Contains(dbGranted, n) {
			dbDenied = append(dbDenied, n)
		}
	}
	schemaGranted, schemaDenied := []string(nil), []string{"sales"}
	if alterOnSchema {
		schemaGranted, schemaDenied = schemaDenied, nil
	}
	resp := capabilityResponsesWithSchemas(true, nil, nil, dbGranted, dbDenied, schemaGranted, schemaDenied)
	for i, r := range resp {
		if r.match != "IS_ROLEMEMBER" {
			continue
		}
		for tag, v := range objRows {
			r.rows = append(r.rows, []driver.Value{tag, "sales.Thing", v})
		}
		resp[i] = r
	}
	sc, _ := newFakeConn(t, resp...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return sc
}

// moveItem is the Move to Schema item objectOpsMenuItems builds for one family
// on sc, and whether it is enabled. It drives the menu rather than
// gate.AllowsOn, because a right set that is never threaded to the call site
// changes nothing and every set-shaped test still passes.
func moveItem(t *testing.T, sc *db.ServerConn, typ NodeType) (controls.MenuItem, bool) {
	t.Helper()
	node := &explorerNode{data: nodeData{
		Type: typ, Name: "Thing", Schema: "sales", DBName: "appdb", conn: sc,
	}}
	for _, it := range (&App{}).objectOpsMenuItems(node) {
		if it.Label == "Move to Schema..." {
			return it, it.Enabled == nil || it.Enabled()
		}
	}
	t.Fatalf("%v offers no Move to Schema item", typ)
	return controls.MenuItem{}, false
}

// TestTheClassOneTransferGateMatchesWhatTheServerAllowed is the live table
// above, run against every family that shares the set. One set serves all of
// them because the class is what the server checks, so a family that quietly
// fell back to the Rename/Delete set fails here rather than at the next live
// run.
func TestTheClassOneTransferGateMatchesWhatTheServerAllowed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		dbGranted     []string
		alterOnSchema bool
		objRows       map[string]int64
		want          bool
	}{
		{"ALTER on the object", nil, false, map[string]int64{"O:ALTER": 1}, false},
		// A CONTROL grant produces both rows: gosmo's object block matches
		// CONTROL alongside whatever name it asks about.
		{"CONTROL on the object, or its ownership", nil, false,
			map[string]int64{"O:ALTER": 1, "O:CONTROL": 1}, true},
		{"CONTROL on the object, DENY ALTER on it", nil, false,
			map[string]int64{"O:ALTER": 0, "O:CONTROL": 1}, true},
		{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, true,
			map[string]int64{"O:ALTER": 1, "O:CONTROL": 1}, true},
		{"db_ddladmin", []string{"ALTER ANY SCHEMA"}, true, map[string]int64{"O:ALTER": 1}, false},
		{"ALTER on the database", []string{"ALTER", "ALTER ANY SCHEMA"}, true,
			map[string]int64{"O:ALTER": 1}, false},
		{"ALTER on the source schema", nil, true, map[string]int64{"O:ALTER": 1}, false},
		{"CONTROL on the database, DENY CONTROL on the object",
			[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, true,
			map[string]int64{"O:ALTER": 0, "O:CONTROL": 0}, false},
	} {
		sc := classOneConn(t, tc.dbGranted, tc.alterOnSchema, tc.objRows)
		for _, typ := range classOneTransferFamilies {
			if _, got := moveItem(t, sc, typ); got != tc.want {
				t.Errorf("%s, %v: Move to Schema enabled = %v, want %v", tc.name, typ, got, tc.want)
			}
		}
	}
}

// TestAWithheldClassOneMoveNamesTheObject. The set's first right is CONTROL on
// the object, and "needs CONTROL" alone reads as CONTROL on the *database* —
// which sends a db_ddladmin member after something far wider than the one
// grant that is missing.
func TestAWithheldClassOneMoveNamesTheObject(t *testing.T) {
	sc := classOneConn(t, []string{"ALTER ANY SCHEMA"}, true, map[string]int64{"O:ALTER": 1})
	for _, typ := range classOneTransferFamilies {
		it, enabled := moveItem(t, sc, typ)
		if enabled {
			t.Errorf("%v: Move to Schema offered to a db_ddladmin member, who was refused it live", typ)
			continue
		}
		if want := "needs CONTROL on the object itself"; it.Note != want {
			t.Errorf("%v: withheld move's note = %q, want %q", typ, it.Note, want)
		}
		if it.NoteWhen == nil || !it.NoteWhen() {
			t.Errorf("%v: withheld move did not show its note", typ)
		}
	}
}

// TestNoClassOneTransferSetAsksAboutAlter. The set-shaped half of the table:
// every right the live run refused must be absent, not merely outvoted. A set
// carrying gate.AlterOnObject offers the move to every ALTER holder, because
// the O:ALTER map reads 1 for a CONTROL grant too and cannot tell them apart.
func TestNoClassOneTransferSetAsksAboutAlter(t *testing.T) {
	for _, typ := range classOneTransferFamilies {
		rights := objectTransferRights(nodeData{Type: typ, Schema: "sales", Name: "Thing", DBName: "appdb"})
		for _, r := range rights {
			for _, refused := range []gate.Right{
				gate.AlterOnObject, gate.AlterOnSchema, gate.AlterAnySchema, gate.AlterDatabase,
			} {
				if sameRight(r, refused) {
					t.Errorf("%v: Move to Schema asks about %q, which the server refused the transfer to",
						typ, r.String())
				}
			}
		}
		if !slicesContainsRight(rights, gate.ControlOnObject) {
			t.Errorf("%v: Move to Schema never asks about CONTROL on the object, the right that permits it", typ)
		}
	}
}
