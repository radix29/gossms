package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// secPolicyConn is a login answering the database probe of appdb: dbGranted
// held, every other right the policy's two groups could be answered by read 0
// at database scope, and the schemas as given. denySchemas adds the catalog's
// DENY rows, which is what the server records for DENY ALTER ON SCHEMA::s.
func secPolicyConn(t *testing.T, dbGranted, schemaGranted, schemaDenied, denySchemas []string) *db.ServerConn {
	t.Helper()
	var dbDenied []string
	for _, n := range []string{"ALTER ANY SECURITY POLICY", "ALTER", "CONTROL", "ALTER ANY SCHEMA"} {
		if !slices.Contains(dbGranted, n) {
			dbDenied = append(dbDenied, n)
		}
	}
	responses := capabilityResponsesWithSchemas(true, nil, nil, dbGranted, dbDenied, schemaGranted, schemaDenied)
	sc, _ := newFakeConn(t, withDeniedSchemas(responses, denySchemas...)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return sc
}

func secPolicyNode(sc *db.ServerConn) *explorerNode {
	return &explorerNode{data: nodeData{
		Type: NodeSecurityPolicy, Schema: "rls", Name: "pol", DBName: "appdb",
		IsEnabled: true, conn: sc,
	}}
}

// contextItemNamed is itemNamed over the whole context menu, which is where
// Delete is spliced in beside the node's own items.
func contextItemNamed(t *testing.T, node *explorerNode, label string) controls.MenuItem {
	t.Helper()
	items := (&App{}).contextMenuItemsForNode(node)
	for _, it := range items {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("%v menu = %v, want an item %q", node.data.Type, labelsOf(items), label)
	return controls.MenuItem{}
}

// TestASecurityPolicyNeedsBothHalves. DROP SECURITY POLICY and ALTER SECURITY
// POLICY ... WITH (STATE = OFF) each need ALTER ANY SECURITY POLICY *and*
// ALTER on the policy's schema, and are refused with either missing — probed
// live 2026-09-11 on 13 and 17, one row here per live case that separates
// them. Asked any-of, the policy right alone offered both items to a principal
// the server then refused (Msg 3701, Msg 33268).
func TestASecurityPolicyNeedsBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		dbGranted, sGrant, sDenied []string
		denySchema                 []string
		offered                    bool
		note                       string
	}{
		{name: "policy right and ALTER on the schema",
			dbGranted: []string{"ALTER ANY SECURITY POLICY"}, sGrant: []string{"rls"},
			offered: true},
		{name: "policy right, schema reads ALTER through a database-wide ALTER",
			dbGranted: []string{"ALTER ANY SECURITY POLICY", "ALTER"}, sGrant: []string{"rls"},
			offered: true},
		{name: "policy right alone",
			dbGranted: []string{"ALTER ANY SECURITY POLICY"}, sDenied: []string{"rls"},
			note: "needs ALTER on the object's schema"},
		{name: "policy right and ALTER on another schema",
			dbGranted: []string{"ALTER ANY SECURITY POLICY"}, sGrant: []string{"other"}, sDenied: []string{"rls"},
			note: "needs ALTER on the object's schema"},
		{name: "ALTER on the schema alone",
			sGrant: []string{"rls"},
			note:   "needs ALTER ANY SECURITY POLICY"},
		{name: "neither",
			sDenied: []string{"rls"},
			note:    "needs ALTER ANY SECURITY POLICY"},
		// The live c11 row: a database-wide ALTER holds ALTER on every schema
		// but the denied one, and the DENY wins — named, since no grant the
		// note could suggest would help.
		{name: "policy right and database ALTER, schema denied",
			dbGranted: []string{"ALTER ANY SECURITY POLICY", "ALTER"}, sDenied: []string{"rls"},
			denySchema: []string{"rls"},
			note:       "ALTER denied on schema rls"},
		// A schema the probe never answered — created since — is unknown, and
		// unknown fails open.
		{name: "policy right, schema unprobed",
			dbGranted: []string{"ALTER ANY SECURITY POLICY"},
			offered:   true},
	} {
		sc := secPolicyConn(t, tc.dbGranted, tc.sGrant, tc.sDenied, tc.denySchema)
		for _, label := range []string{"Delete...", "Disable"} {
			item := contextItemNamed(t, secPolicyNode(sc), label)
			if got := itemEnabled(item); got != tc.offered {
				t.Errorf("%s: %q offered = %v, want %v", tc.name, label, got, tc.offered)
			}
			if tc.offered {
				continue
			}
			if item.NoteWhen == nil || !item.NoteWhen() || item.Note != tc.note {
				t.Errorf("%s: %q note = %q, want %q", tc.name, label, item.Note, tc.note)
			}
		}
	}
}

// TestADetailBrowserPolicySelectionNeedsBothHalves is the same conjunction
// through the Details pane's multi-row Delete, which asks the rights per row
// and must name the row whose schema is the missing half.
func TestADetailBrowserPolicySelectionNeedsBothHalves(t *testing.T) {
	sc := secPolicyConn(t, []string{"ALTER ANY SECURITY POLICY"}, []string{"rls"}, []string{"audit"}, nil)
	objs := []nodeData{
		{Type: NodeSecurityPolicy, Schema: "rls", Name: "pol", DBName: "appdb"},
		{Type: NodeSecurityPolicy, Schema: "audit", Name: "pol2", DBName: "appdb"},
	}
	item := gateDeleteSelection(controls.MenuItem{Label: "Delete"}, sc, objs)
	if itemEnabled(item) {
		t.Error("Delete was offered on a selection with a policy in a schema the login has no ALTER on")
	}
	if want := "audit.pol2 needs ALTER on the object's schema"; item.Note != want {
		t.Errorf("note = %q, want %q", item.Note, want)
	}

	item = gateDeleteSelection(controls.MenuItem{Label: "Delete"}, sc, objs[:1])
	if !itemEnabled(item) {
		t.Error("Delete was withheld on a policy whose both halves are held")
	}
}

// TestGateOnKeepsItsOneGroupNote pins the refactor gateOn went through: as the
// one-group case of gateOnAll it must name the set's first right exactly as
// it did before, for a set whose first right is database-wide.
func TestGateOnKeepsItsOneGroupNote(t *testing.T) {
	sc := probedConn(t, "appdb", nil, nil, nil, []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"})
	item := gateOn(controls.MenuItem{Label: "Delete..."}, sc, "appdb", "", "obj", objectWriteRights()...)
	if itemEnabled(item) || item.Note != "needs ALTER" {
		t.Errorf("gateOn note = %q (offered %v), want %q", item.Note, itemEnabled(item), "needs ALTER")
	}
}
