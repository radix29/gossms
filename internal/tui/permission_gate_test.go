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

// TestAWithheldItemDoesNotBlameThePermissionForSomeoneElsesReason. gate ANDs
// its own predicate with whatever the item already had, so an item can be grey
// for a reason the note does not describe. Every AG replica menu is like this:
// the two failover items and Remove Replica carry Enabled: secondary, and on
// the primary they read "needs ALTER ANY AVAILABILITY GROUP" — a right a
// sysadmin holding it in full would go and ask for, to no effect.
func TestAWithheldItemDoesNotBlameThePermissionForSomeoneElsesReason(t *testing.T) {
	// Holds the right; withheld only by the item's own predicate.
	f := probedConn(t, "", []string{"ALTER ANY AVAILABILITY GROUP"}, nil, nil, nil)

	onPrimary := gate(controls.MenuItem{
		Label:   "Fail Over to This Replica...",
		Enabled: func() bool { return false },
	}, f, "", rightAlterAnyAG)

	if onPrimary.Enabled() {
		t.Fatal("the item's own predicate was discarded")
	}
	if onPrimary.NoteWhen == nil || onPrimary.NoteWhen() {
		t.Error("the permission note is shown on an item the permission does not withhold")
	}

	// The other direction: the right is denied and the item's own predicate is
	// satisfied, so the note is exactly the reason.
	denied := probedConn(t, "", nil, []string{"ALTER ANY AVAILABILITY GROUP"}, nil, nil)
	onSecondary := gate(controls.MenuItem{
		Label:   "Fail Over to This Replica...",
		Enabled: func() bool { return true },
	}, denied, "", rightAlterAnyAG)

	if onSecondary.Enabled() {
		t.Fatal("the gate did not withhold an action whose right is denied")
	}
	if onSecondary.NoteWhen == nil || !onSecondary.NoteWhen() {
		t.Error("the permission note is withheld on an item the permission is withholding")
	}
}

// TestTheActivityMonitorGateReadsTheConnectionItWouldOpen. The Tools menu item
// and the toolbar button both name no connection: showActivityMonitor resolves
// one with connOrFirst, which falls back to connections[0] when the Object
// Explorer has no selection. The gate read selectedServerConn instead, which
// is nil in exactly that state — so with nothing selected the item was offered
// on a fail-open nil while the action went to a connection whose right is
// denied, and the user got the server's refusal instead of a grey item.
func TestTheActivityMonitorGateReadsTheConnectionItWouldOpen(t *testing.T) {
	a := newTestApp()
	// Not addTestConn: registering a root selects it, and the state this is
	// about is a connection open with nothing selected in the tree.
	denied := probedConn(t, "", nil,
		[]string{"VIEW SERVER STATE", "VIEW SERVER PERFORMANCE STATE", "VIEW SERVER SECURITY STATE"},
		nil, nil)
	a.connections = append(a.connections, denied)

	if a.explorer.Selected() != nil {
		t.Fatal("the fixture selected a node; this test is about nothing being selected")
	}
	if got := a.activeServerConn(); got != denied {
		t.Fatalf("activeServerConn = %v, want the connection connOrFirst would return", got)
	}

	// Through the real predicates, not allowsAction directly: the bug was in
	// which connection these two hand it, so a test that picks the connection
	// itself cannot see it.
	if item := menuItemLabelled(t, a.buildMenus(), "Tools", "Activity Monitor"); item.enabledNow() {
		t.Error("the Tools menu offers Activity Monitor on a connection the server has refused")
	}
	if btn := toolbarButtonTipped(t, a.buildToolbar(), "Activity Monitor"); btn.enabledNow() {
		t.Error("the toolbar offers Activity Monitor on a connection the server has refused")
	}

	// The gate and the action must not be able to drift apart again.
	if a.connOrFirst() != a.activeServerConn() {
		t.Error("the action's target and the gate's target are different connections")
	}
}

// menuItemLabelled finds one item by menu header and label.
func menuItemLabelled(t *testing.T, menus []controls.Menu, menu, label string) gatedItem {
	t.Helper()
	for _, m := range menus {
		if m.Label != menu {
			continue
		}
		for _, it := range m.Items {
			if it.Label == label {
				return gatedItem{it.Enabled}
			}
		}
	}
	t.Fatalf("no %q item in the %q menu", label, menu)
	return gatedItem{}
}

// toolbarButtonTipped finds one toolbar button by its tooltip.
func toolbarButtonTipped(t *testing.T, buttons []controls.ToolbarButton, tooltip string) gatedItem {
	t.Helper()
	for _, b := range buttons {
		if b.Tooltip == tooltip {
			return gatedItem{b.Enabled}
		}
	}
	t.Fatalf("no toolbar button tooltipped %q", tooltip)
	return gatedItem{}
}

// gatedItem is whichever of the two carries the predicate under test.
type gatedItem struct{ enabled func() bool }

func (g gatedItem) enabledNow() bool { return g.enabled == nil || g.enabled() }

// TestActiveServerConnDoesNotTouchTheStatusBar. It is called from an Enabled
// predicate, which runs while a menu is being drawn — connOrFirst reports a
// missing connection there, and doing that on every frame would overwrite
// whatever the status bar was saying.
func TestActiveServerConnDoesNotTouchTheStatusBar(t *testing.T) {
	a := newTestApp()
	a.setStatus("something the user needs to read")

	if got := a.activeServerConn(); got != nil {
		t.Fatalf("activeServerConn = %v with nothing connected, want nil", got)
	}
	if a.statusText != "something the user needs to read" {
		t.Errorf("statusText = %q: the gate overwrote the status bar", a.statusText)
	}

	// connOrFirst is the arm that does report it, and still must.
	if a.connOrFirst() != nil || a.statusText != notConnectedMessage {
		t.Errorf("connOrFirst left statusText = %q, want %q", a.statusText, notConnectedMessage)
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
