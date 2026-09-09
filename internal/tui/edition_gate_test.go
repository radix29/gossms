package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// probedAzureConn is probedConn for a Managed Instance: every right granted,
// so nothing the tests below see is withheld for a permission reason and the
// edition is the only thing left that can withhold anything.
func probedAzureConn(t *testing.T, dbName string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConnOnAzureMI(t, capabilityResponses(true,
		[]string{"ALTER ANY DATABASE", "CREATE ANY DATABASE", "CONTROL SERVER"}, nil,
		[]string{"BACKUP DATABASE", "ALTER", "CONTROL"}, nil)...)
	sc.ProbeCapabilities()
	if dbName != "" {
		sc.DatabaseCapabilities(context.Background(), dbName)
	}
	return sc
}

func TestTheEditionGateOnlyFiresOnAzure(t *testing.T) {
	onPrem := probedConn(t, "", []string{"ALTER ANY DATABASE"}, nil, nil, nil)
	item := gateAzure(controls.MenuItem{Label: "Detach Database..."}, onPrem)
	if item.Enabled != nil && !item.Enabled() {
		t.Error("an on-premises instance lost an action to the edition gate")
	}
	if item.Note != "" {
		t.Errorf("Note = %q on an instance the gate does not apply to", item.Note)
	}
	if gateAzure(controls.MenuItem{}, nil).Enabled != nil {
		t.Error("a nil connection was gated — unknown must fail open")
	}
}

// TestAnEditionWithheldItemCarriesTheEditionNote. The note is the only chance
// to say why, and "needs CONTROL" — what the permission gate would have said —
// sends the user after a right that cannot help: MI has no sp_detach_db at all.
// "N/A" says the same thing in the width a menu has.
func TestAnEditionWithheldItemCarriesTheEditionNote(t *testing.T) {
	sc := probedAzureConn(t, "")
	item := gateAzure(gate(controls.MenuItem{Label: "Detach Database..."},
		sc, "", rightControlDB, rightAlterAnyDatabase), sc)

	if item.Enabled() {
		t.Error("Detach Database is still offered on a Managed Instance")
	}
	if item.Note != "N/A" {
		t.Errorf("Note = %q, want the short edition note", item.Note)
	}
	if item.NoteWhen != nil && !item.NoteWhen() {
		t.Error("the edition note is suppressed on the item it describes")
	}
}

// TestAnAzureDatabaseMenuWithholdsWhatTheEditionRefuses is the wiring test:
// every right is granted, so anything grey here is grey for the edition.
func TestAnAzureDatabaseMenuWithholdsWhatTheEditionRefuses(t *testing.T) {
	a := newTestApp()
	sc := probedAzureConn(t, "appdb")
	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}}

	on, off := gatedLabels(a.contextMenuItemsForNode(node))
	for _, want := range []string{"Take Database Offline", "Detach Database..."} {
		if !slices.Contains(off, want) {
			t.Errorf("%q is still offered on a Managed Instance, which refuses it", want)
		}
	}
	for _, want := range []string{"New Query", "View Backup History", "Properties..."} {
		if !slices.Contains(on, want) {
			t.Errorf("%q was withheld, and the edition supports it", want)
		}
	}

	folder := &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases, conn: sc}}
	on, off = gatedLabels(a.contextMenuItemsForNode(folder))
	if !slices.Contains(off, "Attach Database...") {
		t.Error("Attach Database is still offered on a Managed Instance")
	}
	// New Database works there — only its file and filegroup fields do not.
	if !slices.Contains(on, "New Database...") {
		t.Error("New Database was withheld, and a bare CREATE DATABASE works on a Managed Instance")
	}
}

// TestNewDatabaseWithholdsTheFileFieldsOnAzure. CREATE DATABASE works on a
// Managed Instance only with no file or filegroup clause at all (Msg 41918),
// which is what a blank file section emits — so the rows are read-only and the
// dialog stays usable, rather than the whole dialog being withheld.
func TestNewDatabaseWithholdsTheFileFieldsOnAzure(t *testing.T) {
	pf := &ndbPrefetch{
		loginNames:    []string{"sa"},
		modelRecovery: "FULL",
		modelCompat:   160,
		defaultOwner:  "sa",
	}

	azure := probedAzureConn(t, "")
	f, _, _ := buildNewDatabaseGeneralPage(azure, pf)
	for _, label := range []string{"Logical name", "Path", "Initial size", "Growth", "Recovery model"} {
		if focusableRowNamed(f, label) {
			t.Errorf("%q is still editable on a Managed Instance", label)
		}
	}
	// The identity fields are the whole point of the page and must survive.
	for _, label := range []string{"Database name", "Collation", "Compatibility level"} {
		if !focusableRowNamed(f, label) {
			t.Errorf("%q was withheld, and a Managed Instance accepts it", label)
		}
	}

	onPrem := probedConn(t, "", nil, nil, nil, nil)
	f, _, _ = buildNewDatabaseGeneralPage(onPrem, pf)
	for _, label := range []string{"Logical name", "Path", "Recovery model"} {
		if !focusableRowNamed(f, label) {
			t.Errorf("%q lost its editor on an on-premises instance", label)
		}
	}
}

// focusableRowNamed reports whether the form has an editable row with this
// label. A row made read-only stops being focusable, which is the observable
// difference the edition gate makes.
func focusableRowNamed(f *propsheet.Form, label string) bool {
	for _, r := range f.Rows() {
		type labeled interface{ Label() string }
		l, ok := r.(labeled)
		if !ok || l.Label() != label {
			continue
		}
		if r.Focusable() {
			return true
		}
	}
	return false
}

// TestAnOnPremDatabaseMenuKeepsEverything is the other direction, and the one
// a gate wired to the wrong predicate fails.
func TestAnOnPremDatabaseMenuKeepsEverything(t *testing.T) {
	a := newTestApp()
	sc := probedConn(t, "appdb",
		[]string{"ALTER ANY DATABASE", "CREATE ANY DATABASE", "CONTROL SERVER"}, nil,
		[]string{"BACKUP DATABASE", "ALTER", "CONTROL"}, nil)
	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}}

	if _, off := gatedLabels(a.contextMenuItemsForNode(node)); len(off) != 0 {
		t.Errorf("items withheld on an on-premises instance: %v", off)
	}
}
