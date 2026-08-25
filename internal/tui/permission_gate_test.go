package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// probedConn returns a connection whose capability probe has run, with the
// named server and database permissions answered.
func probedConn(t *testing.T, dbName string, granted, denied, dbGranted, dbDenied []string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, capabilityResponses(true, granted, denied, dbGranted, dbDenied)...)
	sc.ProbeCapabilities()
	if dbName != "" {
		sc.DatabaseCapabilities(context.Background(), dbName)
	}
	return sc
}

// TestAnActionIsWithheldOnlyOnAMeasuredDenial is the rule the whole gate layer
// rests on. Three of these four cases are "unknown" in some form, and every one
// of them must leave the action offered: gating on Has instead would empty the
// menus of a sysadmin whose probe timed out.
func TestAnActionIsWithheldOnlyOnAMeasuredDenial(t *testing.T) {
	denied := probedConn(t, "", nil, []string{"ALTER ANY LOGIN"}, nil, nil)
	if allowsAction(denied, "", rightAlterAnyLogin) {
		t.Error("an action was offered though the server denied the right it needs")
	}

	granted := probedConn(t, "", []string{"ALTER ANY LOGIN"}, nil, nil, nil)
	if !allowsAction(granted, "", rightAlterAnyLogin) {
		t.Error("an action was withheld from a login that holds the right")
	}

	// Never probed, and a right this instance does not define: both unknown.
	unprobed, _ := newFakeConn(t)
	if !allowsAction(unprobed, "", rightAlterAnyLogin) {
		t.Error("an unprobed connection lost an action — unknown must fail open")
	}
	if !allowsAction(denied, "", requiredRight{name: "NO SUCH PERMISSION"}) {
		t.Error("a permission this instance does not define was treated as a denial")
	}
	if allowsAction(nil, "", rightAlterAnyLogin) != true {
		t.Error("a nil connection withheld an action")
	}
}

// TestAnyOneRightIsEnough — the lists are alternatives, not a conjunction:
// dropping a database needs CONTROL on it *or* ALTER ANY DATABASE, and a login
// with the second must keep the item.
func TestAnyOneRightIsEnough(t *testing.T) {
	f := probedConn(t, "appdb", []string{"ALTER ANY DATABASE"}, nil, nil, []string{"CONTROL", "ALTER"})

	if !allowsAction(f, "appdb", rightControlDB, rightAlterAnyDatabase) {
		t.Error("withheld though one of the two rights is granted")
	}
	if allowsAction(f, "appdb", rightControlDB, rightAlterDatabase) {
		t.Error("offered though every right it names is denied")
	}
}

// TestAnInaccessibleDatabaseDeniesEveryDatabaseRight. Such a database answers
// "unknown" to every permission — there was nothing to ask inside it — and
// unknown fails open, which would leave Back Up and Delete offered on exactly
// the databases the login cannot open at all.
func TestAnInaccessibleDatabaseDeniesEveryDatabaseRight(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponses(false, nil, nil, nil, nil)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "backup_test")

	if allowsAction(sc, "backup_test", rightBackupDatabase) {
		t.Error("Back Up was offered on a database this login cannot open")
	}
	// And the fail-open value from a probe that never ran still fails open.
	if !allowsAction(sc, "never_probed", rightBackupDatabase) {
		t.Error("a database nobody has asked about lost an action")
	}
}

// TestADatabaseGateNeverProbes. Enabled predicates run while a menu is being
// drawn, on the UI goroutine; a probe there blocks the whole application on a
// slow server. The gate must read the cache and nothing else.
func TestADatabaseGateNeverProbes(t *testing.T) {
	sc, inst := newFakeConn(t, capabilityResponses(true, nil, nil, nil, []string{"BACKUP DATABASE"})...)
	sc.ProbeCapabilities()

	before := inst.QueryCount()
	if !allowsAction(sc, "appdb", rightBackupDatabase) {
		t.Error("an unprobed database withheld an action")
	}
	if got := inst.QueryCount(); got != before {
		t.Errorf("the gate issued %d queries, want none", got-before)
	}
}

// TestGateKeepsTheExistingPredicate — "no rights" and "nothing selected" are
// both reasons to withhold, and neither may cancel the other out.
func TestGateKeepsTheExistingPredicate(t *testing.T) {
	f := probedConn(t, "", []string{"ALTER ANY LOGIN"}, nil, nil, nil)

	item := gate(controls.MenuItem{Label: "New Login...", Enabled: func() bool { return false }},
		f, "", rightAlterAnyLogin)
	if item.Enabled() {
		t.Error("gate discarded the predicate the item already had")
	}
}

// TestAWithheldItemSaysWhat. A disabled menu item cannot be selected or
// clicked, so there is no keypress left to answer with a status message — the
// note in the shortcut column is the only chance to say why.
func TestAWithheldItemSaysWhat(t *testing.T) {
	f := probedConn(t, "", nil, []string{"ALTER ANY LOGIN"}, nil, nil)

	item := gate(controls.MenuItem{Label: "New Login..."}, f, "", rightAlterAnyLogin)
	if !strings.Contains(item.Note, "ALTER ANY LOGIN") {
		t.Errorf("Note = %q, want the missing right named", item.Note)
	}
}

// TestRequiresTextNamesTheRoleToo, because "ALTER SETTINGS" is not what an
// administrator grants — serveradmin is.
func TestRequiresTextNamesTheRoleToo(t *testing.T) {
	got := requiresText(rightAlterSettings)
	for _, want := range []string{"ALTER SETTINGS", "serveradmin"} {
		if !strings.Contains(got, want) {
			t.Errorf("requiresText = %q, want it to mention %q", got, want)
		}
	}
	if got := requiresText(rightControlDB, rightAlterAnyDatabase); !strings.Contains(got, " or ") {
		t.Errorf("requiresText = %q, want alternatives joined with \"or\"", got)
	}
}

// -- the read-only Properties page -------------------------------------------

// TestPageReadOnlyReasonNamesTheRight, and stays silent whenever the answer
// was not measured.
func TestPageReadOnlyReasonNamesTheRight(t *testing.T) {
	ctx := context.Background()

	denied := probedConn(t, "", nil, []string{"ALTER SETTINGS"}, nil, nil)
	page := withRequires(propPage{title: "Memory"}, "", rightAlterSettings)
	got := pageReadOnlyReason(ctx, denied, page)
	if !strings.Contains(got, "ALTER SETTINGS") {
		t.Errorf("reason = %q, want the right named", got)
	}

	granted := probedConn(t, "", []string{"ALTER SETTINGS"}, nil, nil, nil)
	if got := pageReadOnlyReason(ctx, granted, page); got != "" {
		t.Errorf("reason = %q for a login that holds the right, want none", got)
	}

	unprobed, _ := newFakeConn(t)
	if got := pageReadOnlyReason(ctx, unprobed, page); got != "" {
		t.Errorf("reason = %q without a probe, want none — unknown must fail open", got)
	}

	if got := pageReadOnlyReason(ctx, denied, propPage{title: "General"}); got != "" {
		t.Errorf("reason = %q for a page that declares no rights, want none", got)
	}
}

// TestADatabasePageIsReadOnlyOnAnInaccessibleDatabase. Database Properties
// opens on one and every read fails; leaving Apply live on top of that is the
// worst combination on offer.
func TestADatabasePageIsReadOnlyOnAnInaccessibleDatabase(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponses(false, nil, []string{"ALTER ANY DATABASE"}, nil, nil)...)
	sc.ProbeCapabilities()

	page := withRequires(propPage{title: "Options"}, "backup_test", databaseWriteRights()...)
	if got := pageReadOnlyReason(context.Background(), sc, page); got == "" {
		t.Error("a page on a database this login cannot open is still writable")
	}

	// The server-scope alternative genuinely does apply to a database the
	// login cannot connect to — ALTER DATABASE ... SET OFFLINE needs no
	// CONNECT — so holding it must keep the page writable.
	held, _ := newFakeConn(t, capabilityResponses(false, []string{"ALTER ANY DATABASE"}, nil, nil, nil)...)
	held.ProbeCapabilities()
	if got := pageReadOnlyReason(context.Background(), held, page); got != "" {
		t.Errorf("reason = %q though the login holds ALTER ANY DATABASE", got)
	}
}

// -- the menus themselves ----------------------------------------------------

// gatedLabels returns the labels of the items in items that are enabled right
// now, and of those that are not.
func gatedLabels(items []controls.MenuItem) (on, off []string) {
	for _, it := range items {
		if it.Divider {
			continue
		}
		if it.Enabled == nil || it.Enabled() {
			on = append(on, it.Label)
		} else {
			off = append(off, it.Label)
		}
	}
	return on, off
}

// TestADatabaseMenuWithholdsTheWritesTheLoginCannotDo is the wiring test: the
// helpers above prove the predicate, this proves it is actually attached.
// Right-clicking a database a login can only read offered Back Up, Restore,
// Take Offline, Rename and Delete, every one of which fails at the server.
func TestADatabaseMenuWithholdsTheWritesTheLoginCannotDo(t *testing.T) {
	a := newTestApp()
	sc := probedConn(t, "appdb", nil,
		[]string{"ALTER ANY DATABASE", "CREATE ANY DATABASE"},
		nil, []string{"BACKUP DATABASE", "ALTER", "CONTROL", "ALTER ANY SCHEMA"})
	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}}

	on, off := gatedLabels(a.contextMenuItemsForNode(node))
	for _, want := range []string{"Back Up Database...", "Restore Database...", "Take Database Offline"} {
		if !slices.Contains(off, want) {
			t.Errorf("%q is still offered to a login denied every right it needs", want)
		}
	}
	// Reading is untouched.
	for _, want := range []string{"New Query", "View Backup History", "Properties..."} {
		if !slices.Contains(on, want) {
			t.Errorf("%q was withheld, and it only reads", want)
		}
	}
}

// TestADatabaseMenuKeepsEverythingForAnAdministrator is the other direction,
// and the one a too-eager gate fails.
func TestADatabaseMenuKeepsEverythingForAnAdministrator(t *testing.T) {
	a := newTestApp()
	sc := probedConn(t, "appdb",
		[]string{"ALTER ANY DATABASE", "CREATE ANY DATABASE", "CONTROL SERVER"}, nil,
		[]string{"BACKUP DATABASE", "ALTER", "CONTROL"}, nil)
	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", conn: sc}}

	_, off := gatedLabels(a.contextMenuItemsForNode(node))
	if len(off) != 0 {
		t.Errorf("items withheld from a login that holds every right: %v", off)
	}
}

// TestTheLoginsFolderWithholdsNewLogin covers the server scope, which needs no
// cache priming at all.
func TestTheLoginsFolderWithholdsNewLogin(t *testing.T) {
	a := newTestApp()
	denied := probedConn(t, "", nil, []string{"ALTER ANY LOGIN"}, nil, nil)
	node := &explorerNode{label: "Logins", data: nodeData{Type: NodeLogins, conn: denied}}

	_, off := gatedLabels(a.contextMenuItemsForNode(node))
	if !slices.Contains(off, "New Login...") {
		t.Error("New Login... is offered to a login without ALTER ANY LOGIN")
	}

	granted := probedConn(t, "", []string{"ALTER ANY LOGIN"}, nil, nil, nil)
	node.data.conn = granted
	if on, _ := gatedLabels(a.contextMenuItemsForNode(node)); !slices.Contains(on, "New Login...") {
		t.Error("New Login... was withheld from a securityadmin")
	}
}
