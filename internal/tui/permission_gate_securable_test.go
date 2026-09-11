package tui

import (
	"context"
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// The class 5/6/10 gate — assemblies, user-defined types and XML schema
// collections — against the table probed live on majors 13, 14 and 17
// (2026-09-11, identical on all three) with a WITHOUT LOGIN user per case. Each
// row below is one of those users, as gosmo's probe reads it back, and what the
// server let it do.

// securableFamily is one node type in the three classes, with the key gosmo
// files its CONTROL answer under.
type securableFamily struct {
	typ          NodeType
	kind         gosmo.DatabaseSecurableKind
	schema, name string
	word         string // what the Move to Schema note calls it
}

var securableFamilies = []securableFamily{
	{NodeUserDefinedDataType, gosmo.DatabaseSecurableType, "s1", "x", "type"},
	{NodeUserDefinedTableType, gosmo.DatabaseSecurableType, "s1", "x", "type"},
	{NodeUserDefinedType, gosmo.DatabaseSecurableType, "s1", "x", "type"},
	{NodeXmlSchemaCollection, gosmo.DatabaseSecurableXmlSchemaCollection, "s1", "x", "XML schema collection"},
	{NodeAssembly, gosmo.DatabaseSecurableAssembly, "", "a1", "assembly"},
}

// securableConn is a probed connection to appdb in which the principal holds
// the database-scope rights in dbGranted and no other that any of these sets
// names, ALTER on schema s1 only if alterOnSchema, and CONTROL on the
// securable only if control. Every name is answered, as a real probe answers
// it: one left out would read unknown and fail open, and the case would pass
// for that reason instead of the one it names.
func securableConn(t *testing.T, f securableFamily, dbGranted []string, alterOnSchema, control bool) *db.ServerConn {
	t.Helper()
	var dbDenied []string
	for _, n := range []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA", "ALTER ANY ASSEMBLY"} {
		if !slices.Contains(dbGranted, n) {
			dbDenied = append(dbDenied, n)
		}
	}
	schemaGranted, schemaDenied := []string(nil), []string{"s1"}
	if alterOnSchema {
		schemaGranted, schemaDenied = schemaDenied, nil
	}
	responses := withSecurableAnswers(
		capabilityResponsesWithSchemas(true, nil, nil, dbGranted, dbDenied, schemaGranted, schemaDenied),
		map[string]bool{gosmo.DatabaseSecurableKey(f.kind, f.schema, f.name): control})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return sc
}

// TestTheSecurableGateMatchesWhatTheServerAllowed is the live table, per
// family, through objectOpsMenuItems — the items the tree actually builds, so a
// builder asking the wrong securable or the wrong set fails here.
//
// Two columns, because the server answers two questions: the drop and the
// rename (sp_rename's USERDATATYPE for an alias type) go through under either
// CONTROL on the securable or the wider rights, while ALTER SCHEMA ... TRANSFER
// goes through under CONTROL alone. DENY CONTROL has no row: it hides the
// securable from the principal, so there is no node for a gate to withhold.
func TestTheSecurableGateMatchesWhatTheServerAllowed(t *testing.T) {
	type answer struct{ write, move bool }
	for _, tc := range []struct {
		name          string
		dbGranted     []string
		alterOnSchema bool
		control       bool
		// assembly and the rest differ only where ALTER on a schema is
		// concerned: an assembly has no schema, and ALTER ANY SCHEMA
		// permits no DROP ASSEMBLY.
		schemaFamily, assembly answer
	}{
		{"nothing", nil, false, false, answer{false, false}, answer{false, false}},
		// Also the owner's shape: ownership reads CONTROL 1 and nothing else.
		{"CONTROL on the securable, or its ownership", nil, false, true, answer{true, true}, answer{true, true}},
		// ALTER on the securable alone reads CONTROL 0 and holds nothing
		// wider — refused both, which is why ALTER is not what the probe asks.
		{"ALTER on the securable alone", nil, false, false, answer{false, false}, answer{false, false}},
		{"db_ddladmin", []string{"ALTER ANY SCHEMA", "ALTER ANY ASSEMBLY"}, true, false, answer{true, false}, answer{true, false}},
		{"ALTER on the database", []string{"ALTER", "ALTER ANY SCHEMA", "ALTER ANY ASSEMBLY"}, true, false, answer{true, false}, answer{true, false}},
		{"ALTER ANY SCHEMA", []string{"ALTER ANY SCHEMA"}, true, false, answer{true, false}, answer{false, false}},
		{"ALTER on the source schema", nil, true, false, answer{true, false}, answer{false, false}},
		{"ALTER ANY ASSEMBLY", []string{"ALTER ANY ASSEMBLY"}, false, false, answer{false, false}, answer{true, false}},
		// CONTROL on the schema reads ALTER on it as 1 and CONTROL on the
		// securable as 1 — both halves of the table open.
		{"CONTROL on, or ownership of, the source schema", nil, true, true, answer{true, true}, answer{true, true}},
		{"CONTROL on the database", []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA", "ALTER ANY ASSEMBLY"}, true, true, answer{true, true}, answer{true, true}},
	} {
		for _, f := range securableFamilies {
			want := tc.schemaFamily
			if f.typ == NodeAssembly {
				want = tc.assembly
			}
			sc := securableConn(t, f, tc.dbGranted, tc.alterOnSchema, tc.control)
			node := &explorerNode{data: nodeData{Type: f.typ, Schema: f.schema, Name: f.name, DBName: "appdb", conn: sc}}
			items := (&App{}).objectOpsMenuItems(node)
			if len(items) == 0 {
				t.Fatalf("%v: no object-ops items at all", f.typ)
			}
			for _, it := range items {
				got := it.Enabled == nil || it.Enabled()
				expect := want.write
				if it.Label == "Move to Schema..." {
					expect = want.move
				}
				if got != expect {
					t.Errorf("%s, %v: %q enabled = %v, want %v", tc.name, f.typ, it.Label, got, expect)
				}
			}
		}
	}
}

// TestAWithheldMoveNamesTheSecurable. Move to Schema's set is one right, and
// "needs CONTROL" alone reads as CONTROL on the database — which would send a
// db_ddladmin member after something far wider than what is missing.
func TestAWithheldMoveNamesTheSecurable(t *testing.T) {
	for _, f := range securableFamilies {
		if f.typ == NodeAssembly {
			continue // no transfer: an assembly has no schema to move between
		}
		sc := securableConn(t, f, []string{"ALTER ANY SCHEMA"}, true, false)
		node := &explorerNode{data: nodeData{Type: f.typ, Schema: f.schema, Name: f.name, DBName: "appdb", conn: sc}}
		found := false
		for _, it := range (&App{}).objectOpsMenuItems(node) {
			if it.Label != "Move to Schema..." {
				continue
			}
			found = true
			if want := "needs CONTROL on the " + f.word; it.Note != want {
				t.Errorf("%v: withheld move's note = %q, want %q", f.typ, it.Note, want)
			}
		}
		if !found {
			t.Errorf("%v: no Move to Schema item", f.typ)
		}
	}
}

// TestASecurableTheProbeNeverReachedFailsOpen. The map is not sparse, so a
// missing row is a securable created since the probe ran — and unknown fails
// open, the rule the whole layer is built on. Move to Schema has no other right
// to fall back on, so it is the item this bites.
func TestASecurableTheProbeNeverReachedFailsOpen(t *testing.T) {
	f := securableFamilies[0]
	sc := securableConn(t, f, nil, false, false)
	node := &explorerNode{data: nodeData{Type: f.typ, Schema: f.schema, Name: "created_since", DBName: "appdb", conn: sc}}
	for _, it := range (&App{}).objectOpsMenuItems(node) {
		if it.Enabled != nil && !it.Enabled() {
			t.Errorf("%q withheld on a type the probe never asked about", it.Label)
		}
	}
}

// TestATypeIsNotAnsweredByATableOfTheSameName. Types live in their own
// namespace, so s1.x can be a type and a table at once, and the type sets used
// to carry rightAlterOnObject — whose class-1 map records the *table*. A DENY
// on the table then withheld the type's Delete, and a grant on it offered one.
func TestATypeIsNotAnsweredByATableOfTheSameName(t *testing.T) {
	f := securableFamilies[0]
	n := nodeData{Type: f.typ, Schema: f.schema, Name: f.name, DBName: "appdb"}
	// Every wider right answered 0, so only the object and securable arms
	// can decide.
	conn := func(objGranted, objDenied []string, control bool) *db.ServerConn {
		responses := withSecurableAnswers(
			capabilityResponsesWithObjects(true, []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"},
				[]string{f.schema}, objGranted, objDenied),
			map[string]bool{gosmo.DatabaseSecurableKey(f.kind, f.schema, f.name): control})
		sc, _ := newFakeConn(t, responses...)
		sc.ProbeCapabilities()
		sc.DatabaseCapabilities(context.Background(), "appdb")
		return sc
	}
	allows := func(sc *db.ServerConn) bool {
		return allowsActionOn(sc, "appdb", objectDataSchema(n), objectDataObject(n), objectDataRights(n)...)
	}
	if allows(conn([]string{"s1.x"}, nil, false)) {
		t.Error("a GRANT ALTER on table s1.x offered Delete on type s1.x")
	}
	if !allows(conn(nil, []string{"s1.x"}, true)) {
		t.Error("a DENY ALTER on table s1.x withheld Delete on type s1.x, which the login holds CONTROL on")
	}
}
