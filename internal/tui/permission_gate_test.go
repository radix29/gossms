package tui

import (
	"context"
	"database/sql/driver"
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

// TestRequiresTextNamesEachRoleOnce: the alternatives for one action mostly
// share a role, and repeating it made three things to ask for read as six.
func TestRequiresTextNamesEachRoleOnce(t *testing.T) {
	got := requiresText(databaseWriteRights()...)
	want := "Requires ALTER, CONTROL or ALTER ANY DATABASE (db_owner, dbcreator)."
	if got != want {
		t.Errorf("requiresText = %q, want %q", got, want)
	}
	if strings.Count(got, "db_owner") != 1 {
		t.Errorf("requiresText = %q, want db_owner named once", got)
	}
}

// TestRequiresTextKeepsARolelessRightOutOfTheRoleClause. A schema-scoped ALTER
// is granted on the schema and no role carries it, so listing it inside the
// parenthesised roles would send the user after a role that does not confer it.
func TestRequiresTextKeepsARolelessRightOutOfTheRoleClause(t *testing.T) {
	got := requiresText(objectOpRights(NodeTable)...)
	want := "Requires ALTER, CONTROL or ALTER ANY SCHEMA (db_owner, db_ddladmin) " +
		"or ALTER on the object's schema or ALTER on the object itself."
	if got != want {
		t.Errorf("requiresText = %q, want %q", got, want)
	}
	// Alone, each is the whole sentence — no empty parentheses, no stray "or".
	if got, want := requiresText(rightAlterOnSchema), "Requires ALTER on the object's schema."; got != want {
		t.Errorf("requiresText = %q, want %q", got, want)
	}
	if got, want := requiresText(rightAlterOnObject), "Requires ALTER on the object itself."; got != want {
		t.Errorf("requiresText = %q, want %q", got, want)
	}
}

// TestRequiresTextFitsTheReadOnlyBanner. The banner is one line on an 80-column
// terminal's dialog, and the sentence is appended to a 51-character prefix.
func TestRequiresTextFitsTheReadOnlyBanner(t *testing.T) {
	// Only what a page's requires list actually carries: a context menu's
	// rights never reach the banner, they become gateOn's short Note.
	for _, rights := range [][]requiredRight{
		databaseWriteRights(),
		{rightControlServer},
		{rightAlterAnyLogin, rightAlterAnyServerRole},
	} {
		if n := len(readOnlyBannerPrefix + requiresText(rights...)); n > 120 {
			t.Errorf("banner for %v is %d columns, want <= 120", rights, n)
		}
	}

	// The two sets that cannot fit one line, and are allowed not to. A page
	// gated on either names five alternatives or spells out two msdb
	// memberships, and dropping any of them would send a reader after a
	// permission that is not the one they can actually be granted. The note
	// wraps — objectWriteRights() takes three lines in Table Properties'
	// ~83-column form, verified live — so the cost is vertical space. The cap
	// is here because propsheet's Shrinkable clips a note's *trailing* lines
	// when the form is tight, and those are the lines carrying the permission
	// names; the first line, which says the page is read-only at all, always
	// survives.
	for _, rights := range [][]requiredRight{objectWriteRights(), agentWriteRights()} {
		if n := len(readOnlyBannerPrefix + requiresText(rights...)); n > 200 {
			t.Errorf("banner for %v is %d columns, want <= 200", rights, n)
		}
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

// TestAnAlternateSatisfiesADatabaseRight. Every database-scope right declared
// today has an empty alt, so this exercises the branch with a local literal —
// the case it guards against is the next 2022-style permission split landing at
// database scope, which would otherwise be honoured at server scope and
// silently ignored here.
//
// The alternate has to be a name gosmo actually probes at database scope
// (permission_gate_names_test.go says why), so BACKUP LOG stands in for the
// narrower half of a split BACKUP DATABASE.
func TestAnAlternateSatisfiesADatabaseRight(t *testing.T) {
	split := requiredRight{name: "BACKUP DATABASE", role: "db_backupoperator", db: true,
		alt: []string{"BACKUP LOG"}}

	// The named right is denied and the alternate granted: the login can still
	// do the thing, so the action stays.
	holdsAlt := probedConn(t, "appdb", nil, nil, []string{"BACKUP LOG"}, []string{"BACKUP DATABASE"})
	if !allowsAction(holdsAlt, "appdb", split) {
		t.Error("an action was withheld from a login holding the alternate permission")
	}

	// Both denied — the alternates must not turn the gate into a no-op.
	holdsNeither := probedConn(t, "appdb", nil, nil, nil, []string{"BACKUP DATABASE", "BACKUP LOG"})
	if allowsAction(holdsNeither, "appdb", split) {
		t.Error("an action was offered though the named right and its alternate are both denied")
	}

	// Accessibility outranks the alternate: an inaccessible database answered
	// nothing at all, so an unknown alternate must not reopen what Permits
	// closed on the named right.
	sc, _ := newFakeConn(t, capabilityResponses(false, nil, nil, nil, nil)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	if allowsAction(sc, "appdb", split) {
		t.Error("an alternate permission reopened an action on a database the login cannot open")
	}
}

// schemaProbedConn is probedConn for a login whose only rights are on schemas:
// nothing at server scope, nothing database-wide, ALTER on the schemas named.
func schemaProbedConn(t *testing.T, dbName string, schemaGranted, schemaDenied []string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, capabilityResponsesWithSchemas(true, nil, nil, nil,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, schemaGranted, schemaDenied)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), dbName)
	return sc
}

// TestAlterOnOneSchemaKeepsTheObjectOpsInThatSchema. The gap this closes: a
// login granted ALTER on one schema holds no database-wide permission at all,
// so every right the object-ops gate used to ask about reads as denied and
// Rename/Move/Delete disappeared from objects the server would have let it
// rename. The other half matters as much — the items must still go on an
// object in a schema it was not granted.
func TestAlterOnOneSchemaKeepsTheObjectOpsInThatSchema(t *testing.T) {
	sc := schemaProbedConn(t, "appdb", []string{"Sales"}, []string{"dbo"})
	rights := objectOpRights(NodeTable)

	if !allowsActionOn(sc, "appdb", "Sales", "Orders", rights...) {
		t.Error("an object in the granted schema lost its Rename/Move/Delete")
	}
	if allowsActionOn(sc, "appdb", "dbo", "Orders", rights...) {
		t.Error("an object in a schema the login has no ALTER on kept them")
	}
	// A schema nobody probed is unknown, and unknown fails open.
	if !allowsActionOn(sc, "appdb", "Archive", "Orders", rights...) {
		t.Error("an unprobed schema withheld the items — unknown must fail open")
	}
	// And a database-wide right still answers on its own, with no schema.
	wide := probedConn(t, "appdb", nil, nil, []string{"ALTER"}, nil)
	if !allowsActionOn(wide, "appdb", "dbo", "Orders", rights...) {
		t.Error("a database-wide ALTER stopped permitting the object ops")
	}
}

// TestASchemaScopedRightGrantsNothingWithoutASchema. A schema-scoped right is
// asked about only when the caller says which schema; with none it must add
// nothing — neither withholding what the database-wide rights allow, nor
// offering what they deny.
func TestASchemaScopedRightGrantsNothingWithoutASchema(t *testing.T) {
	sc := schemaProbedConn(t, "appdb", []string{"Sales"}, nil)

	if allowsAction(sc, "appdb", rightAlterOnSchema) {
		t.Error("a schema-scoped right answered yes with no schema to ask about")
	}
	if allowsActionOn(sc, "", "Sales", "Orders", rightAlterOnSchema) {
		t.Error("a schema-scoped right answered yes with no database to ask in")
	}
}

// TestObjectOpsOnASchemaNodeIgnoreItsOwnName. ALTER on a schema does not
// permit dropping or renaming the schema itself — that is CONTROL on it, or
// ALTER ANY SCHEMA. Answering with the node's own name would offer three items
// the server then refuses.
func TestObjectOpsOnASchemaNodeIgnoreItsOwnName(t *testing.T) {
	schemaNode := &explorerNode{data: nodeData{Type: NodeSchema, Name: "Sales", Schema: "Sales", DBName: "appdb"}}
	if got := objectOpSchema(schemaNode); got != "" {
		t.Errorf("objectOpSchema on a schema node = %q, want \"\"", got)
	}
	table := &explorerNode{data: nodeData{Type: NodeTable, Name: "Orders", Schema: "Sales", DBName: "appdb"}}
	if got := objectOpSchema(table); got != "Sales" {
		t.Errorf("objectOpSchema on a table = %q, want its schema", got)
	}
}

// TestTheMenuItemsThemselvesFollowTheSchemaGrant — through objectOpsMenuItems,
// not allowsActionOn: the gate is only useful if the items the tree builds ask
// about the schema the node is in, and a builder that passed "" would pass
// every test above.
func TestTheMenuItemsThemselvesFollowTheSchemaGrant(t *testing.T) {
	sc := schemaProbedConn(t, "appdb", []string{"Sales"}, []string{"dbo"})
	a := &App{}

	for _, tt := range []struct {
		schema string
		want   bool
	}{{"Sales", true}, {"dbo", false}} {
		node := &explorerNode{data: nodeData{
			Type: NodeTable, Name: "Orders", Schema: tt.schema, DBName: "appdb", conn: sc,
		}}
		items := a.objectOpsMenuItems(node)
		if len(items) == 0 {
			t.Fatalf("schema %s: a table offered no object-ops items at all", tt.schema)
		}
		for _, it := range items {
			if got := it.Enabled == nil || it.Enabled(); got != tt.want {
				t.Errorf("schema %s: %q enabled = %v, want %v", tt.schema, it.Label, got, tt.want)
			}
		}
	}
}

// TestAnInaccessibleDatabaseWithholdsTheSchemaScopedRightToo. A database the
// login cannot open answers unknown for every schema, because there was
// nothing inside it to ask — and unknown fails open everywhere else. The
// accessibility fold is what stops Rename/Move/Delete being offered on the
// objects of a database that cannot even be connected to; the schema-scoped
// right needs it as much as the database-wide ones do.
func TestAnInaccessibleDatabaseWithholdsTheSchemaScopedRightToo(t *testing.T) {
	sc, _ := newFakeConn(t, capabilityResponsesWithSchemas(false, nil, nil, nil, nil, nil, nil)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "shut")

	if allowsActionOn(sc, "shut", "Sales", "Orders", rightAlterOnSchema) {
		t.Error("a schema-scoped right was allowed in a database the login cannot open")
	}
	if allowsActionOn(sc, "shut", "Sales", "Orders", objectOpRights(NodeTable)...) {
		t.Error("the object ops were offered in a database the login cannot open")
	}
}

// agentConn returns a connection whose server probe has run and whose msdb
// probe has answered the given role memberships.
func agentConn(t *testing.T, granted, denied, roleIn, roleNotIn []string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, capabilityResponsesWithRoles(true, granted, denied, roleIn, roleNotIn)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "msdb")
	return sc
}

// TestAnUnprobedMsdbKeepsEverySQLAgentAction is the case a membership right
// exists to get right, and the only one the permission rights cannot fail on.
//
// InRole answers false for a role that was never asked about exactly as it
// does for one the login is not in. Believing that false would withhold New
// Job, New Schedule, New Alert and New Operator from every login on every
// connection whose msdb probe had not landed — including the sysadmin's, since
// a sysadmin is not a member of any SQLAgent* role either (verified live
// 2026-08-27). Only gosmo's DatabaseCapabilities.Probed separates the two.
func TestAnUnprobedMsdbKeepsEverySQLAgentAction(t *testing.T) {
	// Probed at server scope and denied everything there, so nothing but the
	// membership rights can be what keeps the action.
	sc, _ := newFakeConn(t, capabilityResponses(true, nil, []string{"CONTROL SERVER"}, nil, nil)...)
	sc.ProbeCapabilities()

	if !allowsAction(sc, "", agentWriteRights()...) {
		t.Error("a connection whose msdb probe has not run lost every SQL Agent action")
	}
	if !allowsAction(nil, "", agentWriteRights()...) {
		t.Error("a nil connection withheld the SQL Agent actions")
	}
}

// TestSQLAgentActionsFollowMsdbRoleMembership. The narrowest role is the whole
// test: IS_ROLEMEMBER resolves the nesting, so a member of
// SQLAgentOperatorRole reads 1 for SQLAgentUserRole (verified live
// 2026-08-27), and Reader/Operator need no separate check.
func TestSQLAgentActionsFollowMsdbRoleMembership(t *testing.T) {
	in := agentConn(t, nil, []string{"CONTROL SERVER"},
		[]string{"SQLAgentUserRole"}, []string{"db_owner"})
	if !allowsAction(in, "", agentWriteRights()...) {
		t.Error("a member of SQLAgentUserRole was refused the SQL Agent actions")
	}

	out := agentConn(t, nil, []string{"CONTROL SERVER"},
		nil, []string{"SQLAgentUserRole", "db_owner"})
	if allowsAction(out, "", agentWriteRights()...) {
		t.Error("a login in no msdb Agent role kept the SQL Agent actions")
	}

	owner := agentConn(t, nil, []string{"CONTROL SERVER"},
		[]string{"db_owner"}, []string{"SQLAgentUserRole"})
	if !allowsAction(owner, "", agentWriteRights()...) {
		t.Error("msdb db_owner was refused the SQL Agent actions")
	}
}

// TestASysadminKeepsTheSQLAgentActions. A sysadmin maps to dbo, and dbo is a
// member of no SQLAgent* role — IS_ROLEMEMBER answered 0 for all three on the
// live instance. The set carries CONTROL SERVER for exactly this login, and
// without it the gate would withhold the Agent actions from the one user who
// certainly may perform them.
func TestASysadminKeepsTheSQLAgentActions(t *testing.T) {
	sc := agentConn(t, []string{"CONTROL SERVER"}, nil,
		nil, []string{"SQLAgentUserRole", "db_owner"})
	if !allowsAction(sc, "", agentWriteRights()...) {
		t.Error("a sysadmin lost the SQL Agent actions to its own msdb role membership")
	}
}

// TestTheSQLAgentMenusFollowTheGate drives the menus themselves rather than
// allowsAction, because a gate that is never wired to an item withholds
// nothing and every test above still passes.
func TestTheSQLAgentMenusFollowTheGate(t *testing.T) {
	labels := map[NodeType]string{
		NodeAgentUserJobs:    "New Job...",
		NodeAgentSchedules:   "New Schedule...",
		NodeAgentEventAlerts: "New Alert...",
		NodeAgentOperators:   "New Operator...",
	}
	for nodeType, label := range labels {
		denied := agentConn(t, nil, []string{"CONTROL SERVER"},
			nil, []string{"SQLAgentUserRole", "db_owner"})
		if enabledInMenu(t, denied, nodeType, label) {
			t.Errorf("%s stayed enabled for a login in no msdb Agent role", label)
		}
		allowed := agentConn(t, nil, []string{"CONTROL SERVER"},
			[]string{"SQLAgentUserRole"}, []string{"db_owner"})
		if !enabledInMenu(t, allowed, nodeType, label) {
			t.Errorf("%s was withheld from a member of SQLAgentUserRole", label)
		}
	}
}

// enabledInMenu builds the node's context menu and reports whether the named
// item is enabled. A missing item fails the test rather than reading as
// disabled, which would let a renamed label pass silently.
func enabledInMenu(t *testing.T, sc *db.ServerConn, nodeType NodeType, label string) bool {
	t.Helper()
	app := &App{}
	node := &explorerNode{data: nodeData{Type: nodeType, conn: sc}}
	for _, it := range app.nodeMenuItems(node) {
		if it.Label != label {
			continue
		}
		return it.Enabled == nil || it.Enabled()
	}
	t.Fatalf("%s is not in the menu for node type %v", label, nodeType)
	return false
}

// TestEverySQLAgentNodeTypeIsSeenAsOne pins isAgentNode against the four node
// types whose menus gate on msdb. The range in tree_node.go is contiguous, and
// a new Agent node added outside it would silently stop priming msdb — after
// which the gates read an unprobed database and fail open forever.
func TestEverySQLAgentNodeTypeIsSeenAsOne(t *testing.T) {
	for _, nt := range []NodeType{
		NodeAgentJobs, NodeAgentUserJobs, NodeAgentSchedules,
		NodeAgentEventAlerts, NodeAgentOperators, NodeAgentErrorLog,
	} {
		if !isAgentNode(nt) {
			t.Errorf("node type %v is not recognised as a SQL Agent node", nt)
		}
	}
	for _, nt := range []NodeType{NodeManagement, NodeLinkedServers, NodeDatabase} {
		if isAgentNode(nt) {
			t.Errorf("node type %v was mistaken for a SQL Agent node", nt)
		}
	}
}

// TestTheSQLAgentNoteNamesTheNarrowestRight. gate puts only rights[0] in the
// note, so the order of an alternatives list is what the user is sent to ask
// for. Led with CONTROL SERVER the note read "needs CONTROL SERVER" on the
// live run — telling someone who wants to create a job to go and ask for
// sysadmin, when membership of one msdb role is what they need.
func TestTheSQLAgentNoteNamesTheNarrowestRight(t *testing.T) {
	if got := agentWriteRights()[0].name; got != "SQLAgentUserRole" {
		t.Errorf("the note would say %q; the narrowest sufficient right must come first", got)
	}
	if !slices.ContainsFunc(agentWriteRights(), func(r requiredRight) bool {
		return r.name == "CONTROL SERVER"
	}) {
		t.Error("CONTROL SERVER left the set — a sysadmin is in no SQLAgent* role and would lose the actions")
	}
}

// objectProbedConn returns a connection denied every database- and
// schema-scope right, with only the named objects carrying an explicit grant.
// That is the shape of the principal this right exists for: one granted ALTER
// on a single table and holding nothing at any wider scope.
func objectProbedConn(t *testing.T, dbName string, objGranted, objDenied []string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, capabilityResponsesWithObjects(true,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"},
		[]string{"Sales", "dbo"}, objGranted, objDenied)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), dbName)
	return sc
}

// TestAlterOnOneObjectKeepsTheObjectOpsOnThatObject is the gap this right
// closes, verified live on 2026-08-27: a login granted ALTER on one table
// reads 0 for ALTER at both database and schema scope, so every wider right
// denied and Rename/Move/Delete went from an object SQL Server would have let
// it alter.
func TestAlterOnOneObjectKeepsTheObjectOpsOnThatObject(t *testing.T) {
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)
	rights := objectOpRights(NodeTable)

	if !allowsActionOn(sc, "appdb", "Sales", "Orders", rights...) {
		t.Error("the granted object lost its Rename/Move/Delete")
	}
	// The other half, and the one that makes the right worth having rather
	// than merely harmless: a different object in the same schema must still
	// lose them.
	if allowsActionOn(sc, "appdb", "Sales", "Customers", rights...) {
		t.Error("an object with no grant of its own kept the object ops")
	}
	if allowsActionOn(sc, "appdb", "dbo", "Orders", rights...) {
		t.Error("an object of the same name in another schema kept the object ops")
	}
}

// TestAnObjectRightNeverWidensWhatIsOffered. ObjectPermissions is sparse — an
// object nobody granted anything on has no row — so reading it as "not denied
// means allowed" would permit every write in the database. The right must add
// permission only where a row actually says so.
func TestAnObjectRightNeverWidensWhatIsOffered(t *testing.T) {
	sc := objectProbedConn(t, "appdb", nil, nil)

	if allowsActionOn(sc, "appdb", "Sales", "Orders", rightAlterOnObject) {
		t.Error("an object with no row was treated as granted — the map is sparse, not exhaustive")
	}
	if allowsActionOn(sc, "appdb", "Sales", "Orders", objectOpRights(NodeTable)...) {
		t.Error("adding the object right resurrected object ops the wider rights had withheld")
	}
	// An explicit deny is a row, and must not read as a grant either.
	denied := objectProbedConn(t, "appdb", nil, []string{"Sales.Orders"})
	if allowsActionOn(denied, "appdb", "Sales", "Orders", rightAlterOnObject) {
		t.Error("an explicitly denied object was treated as granted")
	}
}

// TestAnObjectScopedRightGrantsNothingWithoutAnObject — the counterpart to
// TestASchemaScopedRightGrantsNothingWithoutASchema. Asked without the pieces
// it needs, the right must add nothing rather than answer from whatever is at
// hand.
func TestAnObjectScopedRightGrantsNothingWithoutAnObject(t *testing.T) {
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)

	if allowsAction(sc, "appdb", rightAlterOnObject) {
		t.Error("an object-scoped right answered yes with no object to ask about")
	}
	if allowsActionOn(sc, "appdb", "Sales", "", rightAlterOnObject) {
		t.Error("an object-scoped right answered yes with an empty object name")
	}
	if allowsActionOn(sc, "", "Sales", "Orders", rightAlterOnObject) {
		t.Error("an object-scoped right answered yes with no database to ask in")
	}
	if allowsActionOn(sc, "appdb", "", "Orders", rightAlterOnObject) {
		t.Error("an object-scoped right answered yes with no schema to qualify the object")
	}
}

// TestObjectOpsOnASchemaNodeIgnoreItsOwnObjectName. objectOpName excludes a
// schema node for the reason objectOpSchema does: a schema is not an object,
// and passing its name would let a *table* called "Sales" answer for the
// schema "Sales".
func TestObjectOpsOnASchemaNodeIgnoreItsOwnObjectName(t *testing.T) {
	node := &explorerNode{data: nodeData{Type: NodeSchema, Name: "Sales", Schema: "Sales", DBName: "appdb"}}
	if got := objectOpName(node); got != "" {
		t.Errorf("objectOpName on a schema node = %q, want \"\"", got)
	}
	table := &explorerNode{data: nodeData{Type: NodeTable, Name: "Orders", Schema: "Sales", DBName: "appdb"}}
	if got := objectOpName(table); got != "Orders" {
		t.Errorf("objectOpName on a table = %q, want \"Orders\"", got)
	}
}

// TestTheObjectOpMenuItemsFollowTheObjectGrant drives the menu rather than
// allowsActionOn: a right that is never threaded to the call sites changes
// nothing, and every test above still passes.
func TestTheObjectOpMenuItemsFollowTheObjectGrant(t *testing.T) {
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)
	app := &App{}
	enabled := func(name string) map[string]bool {
		node := &explorerNode{data: nodeData{
			Type: NodeTable, Name: name, Schema: "Sales", DBName: "appdb", conn: sc,
		}}
		out := map[string]bool{}
		for _, it := range app.objectOpsMenuItems(node) {
			out[it.Label] = it.Enabled == nil || it.Enabled()
		}
		return out
	}
	granted, other := enabled("Orders"), enabled("Customers")
	if len(granted) == 0 {
		t.Fatal("the object-ops menu is empty; the test is addressing the wrong node type")
	}
	for label, ok := range granted {
		if !ok {
			t.Errorf("%s was withheld on the object the login was granted ALTER on", label)
		}
		if other[label] {
			t.Errorf("%s stayed enabled on an object with no grant of its own", label)
		}
	}
}

// TestAnObjectScopedPageAnswersForTheObject. Before the page's read-only check
// and the menus' gate shared one rule (rightsAllow), this check knew only about
// database- and server-scope rights: a principal granted ALTER on the one table
// reads 0 for every wider permission there is, so every right in
// objectWriteRights() denied and the page they can write opened with a banner
// telling them they cannot.
func TestAnObjectScopedPageAnswersForTheObject(t *testing.T) {
	ctx := context.Background()
	page := withRequiresOn(propPage{title: "Change Tracking"}, "HealthClinic", "dbo", "Patient", objectWriteRights()...)

	denied := []string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}
	granted, _ := newFakeConn(t, capabilityResponsesWithObjects(true, denied, []string{"dbo"}, []string{"dbo.Patient"}, nil)...)
	granted.ProbeCapabilities()
	if got := pageReadOnlyReason(ctx, granted, page); got != "" {
		t.Errorf("reason = %q though the login holds ALTER on dbo.Patient", got)
	}

	// The same login on a table it was not granted: the object map is sparse,
	// so silence there must not be read as a grant.
	other := withRequiresOn(propPage{title: "Change Tracking"}, "HealthClinic", "dbo", "Visit", objectWriteRights()...)
	if got := pageReadOnlyReason(ctx, granted, other); got == "" {
		t.Error("a page on a table with no grant anywhere is still writable")
	}

	elsewhere, _ := newFakeConn(t, capabilityResponsesWithObjects(true, denied, []string{"dbo"}, nil, []string{"dbo.Patient"})...)
	elsewhere.ProbeCapabilities()
	if got := pageReadOnlyReason(ctx, elsewhere, page); got == "" {
		t.Error("a page on an object the login is denied ALTER on is still writable")
	}
}

// TestAnAgentPageAnswersForMsdbMembership. SQL Agent's rights are memberships
// of an msdb role, which HAS_PERMS_BY_NAME cannot be asked about — read as a
// server permission they come back unknown, and the banner never appears for
// anyone.
func TestAnAgentPageAnswersForMsdbMembership(t *testing.T) {
	ctx := context.Background()
	page := withRequires(propPage{title: "General"}, "", agentWriteRights()...)

	member, _ := newFakeConn(t, capabilityResponsesWithRoles(true, nil, []string{"CONTROL SERVER"}, []string{"SQLAgentUserRole"}, []string{"db_owner"})...)
	member.ProbeCapabilities()
	if got := pageReadOnlyReason(ctx, member, page); got != "" {
		t.Errorf("reason = %q for a member of SQLAgentUserRole", got)
	}

	outsider, _ := newFakeConn(t, capabilityResponsesWithRoles(true, nil, []string{"CONTROL SERVER"}, nil, []string{"SQLAgentUserRole", "db_owner"})...)
	outsider.ProbeCapabilities()
	if got := pageReadOnlyReason(ctx, outsider, page); got == "" {
		t.Error("an Agent page is writable for a login in neither msdb role and denied CONTROL SERVER")
	}
}

// serverRoleConn returns a connection whose server probe has answered the
// given fixed-server-role memberships. Roles come back tagged "R" where a
// permission is tagged "P", both out of the same IS_SRVROLEMEMBER query.
func serverRoleConn(t *testing.T, granted, denied, roleIn, roleNotIn []string) *db.ServerConn {
	t.Helper()
	resp := capabilityResponses(true, granted, denied, nil, nil)
	for i, r := range resp {
		if r.match != "IS_SRVROLEMEMBER" {
			continue
		}
		for _, n := range roleIn {
			r.rows = append(r.rows, []driver.Value{"R", n, int64(1)})
		}
		for _, n := range roleNotIn {
			r.rows = append(r.rows, []driver.Value{"R", n, int64(0)})
		}
		resp[i] = r
	}
	sc, _ := newFakeConn(t, resp...)
	sc.ProbeCapabilities()
	return sc
}

// TestBackupDeviceActionsFollowDiskadminMembership. sp_addumpdevice and
// sp_dropdevice are permitted by membership of diskadmin and by no server
// *permission* at all, which is why rightDiskAdmin is a serverRole right.
// Gating on CONTROL SERVER instead would withhold the family from the one
// principal it is for.
func TestBackupDeviceActionsFollowDiskadminMembership(t *testing.T) {
	in := serverRoleConn(t, nil, []string{"CONTROL SERVER"},
		[]string{"diskadmin"}, []string{"sysadmin"})
	if !allowsAction(in, "", rightDiskAdmin) {
		t.Error("a member of diskadmin was refused the backup device actions")
	}

	out := serverRoleConn(t, nil, []string{"CONTROL SERVER"},
		nil, []string{"diskadmin", "sysadmin"})
	if allowsAction(out, "", rightDiskAdmin) {
		t.Error("a login in neither diskadmin nor sysadmin kept the backup device actions")
	}

	// A sysadmin is a member of no other fixed server role — IS_SRVROLEMEMBER
	// answers 0 for diskadmin — while being permitted everything diskadmin
	// carries. Asking only about the named role withholds the family from the
	// one login that certainly may use it.
	admin := serverRoleConn(t, nil, nil, []string{"sysadmin"}, []string{"diskadmin"})
	if !allowsAction(admin, "", rightDiskAdmin) {
		t.Error("a sysadmin lost the backup device actions to its own diskadmin membership")
	}
}

// A server-role right cannot fail open on its own: InServerRole answers false
// for a role never asked about exactly as it does for a login that is not in
// it. Only Capabilities.Probed separates the two, and believing the false
// would withhold New Backup Device from everyone on an unprobed connection.
func TestAnUnprobedServerKeepsTheBackupDeviceActions(t *testing.T) {
	sc, _ := newFakeConn(t) // never probed
	if !allowsAction(sc, "", rightDiskAdmin) {
		t.Error("a connection whose capability probe has not run lost the backup device actions")
	}
	if !allowsAction(nil, "", rightDiskAdmin) {
		t.Error("a nil connection withheld the backup device actions")
	}
}

// A gate that is never wired to the menu item withholds nothing, and every
// test above still passes.
func TestTheNewBackupDeviceMenuItemFollowsTheGate(t *testing.T) {
	denied := serverRoleConn(t, nil, []string{"CONTROL SERVER"},
		nil, []string{"diskadmin", "sysadmin"})
	if enabledInMenu(t, denied, NodeBackupDevices, "New Backup Device...") {
		t.Error("New Backup Device stayed enabled for a login in neither diskadmin nor sysadmin")
	}
	allowed := serverRoleConn(t, nil, []string{"CONTROL SERVER"},
		[]string{"diskadmin"}, []string{"sysadmin"})
	if !enabledInMenu(t, allowed, NodeBackupDevices, "New Backup Device...") {
		t.Error("New Backup Device was withheld from a member of diskadmin")
	}
}

// Enable/Disable on a server trigger is an immediate write ON ALL SERVER, and
// CONTROL SERVER is what SQL Server checks for it. A menu item that is offered
// and then fails is the failure mode the gate exists to prevent — the gate has
// to be wired to the item, which every other test here would still pass
// without.
func TestTheServerTriggerToggleFollowsControlServer(t *testing.T) {
	// enabledInMenu builds a node with IsEnabled unset, so the toggle is
	// labelled Enable.
	denied := serverRoleConn(t, nil, []string{"CONTROL SERVER"}, nil, []string{"sysadmin"})
	if enabledInMenu(t, denied, NodeServerTrigger, "Enable") {
		t.Error("the toggle stayed enabled for a login with neither CONTROL SERVER nor sysadmin")
	}
	allowed := serverRoleConn(t, []string{"CONTROL SERVER"}, nil, nil, []string{"sysadmin"})
	if !enabledInMenu(t, allowed, NodeServerTrigger, "Enable") {
		t.Error("the toggle was withheld from a login holding CONTROL SERVER")
	}
}

// Start/Stop/Disable on an endpoint is an immediate ALTER ENDPOINT, and ALTER
// ANY ENDPOINT is what SQL Server checks for it. A menu item that is offered
// and then fails is the failure mode the gate exists to prevent — and the gate
// has to be wired to all three items, which one test on one item would miss.
func TestTheEndpointStateItemsFollowAlterAnyEndpoint(t *testing.T) {
	denied := serverRoleConn(t, nil, []string{"ALTER ANY ENDPOINT"}, nil, []string{"sysadmin"})
	allowed := serverRoleConn(t, []string{"ALTER ANY ENDPOINT"}, nil, nil, []string{"sysadmin"})
	for _, label := range []string{"Start", "Stop", "Disable"} {
		if enabledInMenu(t, denied, NodeEndpoint, label) {
			t.Errorf("%s stayed enabled for a login with neither ALTER ANY ENDPOINT nor sysadmin", label)
		}
		if !enabledInMenu(t, allowed, NodeEndpoint, label) {
			t.Errorf("%s was withheld from a login holding ALTER ANY ENDPOINT", label)
		}
	}
}

// Enable/Disable on an audit or a specification is an immediate ALTER, and
// ALTER ANY SERVER AUDIT is what SQL Server checks for it — a right nothing
// before this family probed, so a typo here would read CapabilityUnknown
// forever and gate nothing. The New items on both folders take the same right.
func TestTheAuditItemsFollowAlterAnyServerAudit(t *testing.T) {
	denied := serverRoleConn(t, nil, []string{"ALTER ANY SERVER AUDIT"}, nil, []string{"sysadmin"})
	allowed := serverRoleConn(t, []string{"ALTER ANY SERVER AUDIT"}, nil, nil, []string{"sysadmin"})
	for _, tc := range []struct {
		node  NodeType
		label string
	}{
		// enabledInMenu builds a node with IsEnabled unset, so the toggle
		// reads "Enable" on both leaves.
		{NodeAudit, "Enable"},
		{NodeServerAuditSpecification, "Enable"},
		{NodeAudits, "New Audit..."},
		{NodeServerAuditSpecifications, "New Server Audit Specification..."},
	} {
		if enabledInMenu(t, denied, tc.node, tc.label) {
			t.Errorf("%q stayed enabled for a login with neither ALTER ANY SERVER AUDIT nor sysadmin", tc.label)
		}
		if !enabledInMenu(t, allowed, tc.node, tc.label) {
			t.Errorf("%q was withheld from a login holding ALTER ANY SERVER AUDIT", tc.label)
		}
	}
}

// -- Rename/Delete on a node that lives outside a database ----------------------

// deniedEverythingConn is a probed connection holding no server permission, no
// fixed server role and no msdb role — the principal every Rename/Delete on a
// server-level object must be withheld from.
func deniedEverythingConn(t *testing.T) *db.ServerConn {
	t.Helper()
	resp := capabilityResponsesWithRoles(true, nil, serverScopedRightNames(), nil, msdbRoleNames())
	for i, r := range resp {
		if r.match != "IS_SRVROLEMEMBER" {
			continue
		}
		for _, n := range []string{"sysadmin", "diskadmin", "securityadmin"} {
			r.rows = append(r.rows, []driver.Value{"R", n, int64(0)})
		}
		resp[i] = r
	}
	sc, _ := newFakeConn(t, resp...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "msdb")
	return sc
}

func serverScopedRightNames() []string {
	var names []string
	for _, rights := range serverScopedOpRights {
		for _, r := range rights {
			if !r.db && !r.membership && !r.serverRole && !slices.Contains(names, r.name) {
				names = append(names, r.name)
				names = append(names, r.alt...)
			}
		}
	}
	return names
}

func msdbRoleNames() []string {
	var names []string
	for _, rights := range serverScopedOpRights {
		for _, r := range rights {
			if r.membership && !slices.Contains(names, r.name) {
				names = append(names, r.name)
			}
		}
	}
	return names
}

// databaseScopedOpTypes is every objectOps node type that lives inside a
// database, and so carries a DBName the gate can measure. It exists only to be
// the other half of a total classification: a new objectOps entry in neither
// this list nor serverScopedOpRights fails the test below, which is what stops
// the next server-level family arriving with Delete ungated.
var databaseScopedOpTypes = []NodeType{
	NodeDatabase, NodeTable, NodeView, NodeStoredProcedure, NodeFunction,
	NodeTrigger, NodeSequence, NodeSynonym, NodeColumn, NodeIndex,
	NodeStatistic, NodeKey, NodeForeignKey, NodeCheck, NodePartitionFunction,
	NodePartitionScheme, NodeSecurityPolicy, NodeColumnMasterKey,
	NodeColumnEncryptionKey, NodeUser, NodeDatabaseRole, NodeSchema,
}

// TestServerScopedOpsAreGated is the meta-test §2 of the 2026-09-02 review
// asked for. objectOpRights fell through to objectWriteRights() for everything
// but NodeDatabase, and every one of those rights is database-, schema- or
// object-scoped — so with the empty DBName a server-level node carries,
// rightsAllow took its "no database to ask about" branch and answered yes for
// a principal holding nothing. Delete and Rename were offered on every login,
// credential, audit, endpoint and Agent job, then refused by the server.
func TestServerScopedOpsAreGated(t *testing.T) {
	sc := deniedEverythingConn(t)
	for nodeType := range objectOps {
		_, server := serverScopedOpRights[nodeType]
		database := slices.Contains(databaseScopedOpTypes, nodeType)
		switch {
		case server && database:
			t.Errorf("objectOps[%v] is classified both server- and database-scoped", nodeType)
		case !server && !database:
			t.Errorf("objectOps[%v] is in neither serverScopedOpRights nor databaseScopedOpTypes; "+
				"a server-level node with no entry is offered Delete unconditionally", nodeType)
		case !server:
			continue
		}
		// The empty dbName is what the node actually carries, and is the whole
		// bug: asked with it, a database-scoped right set answers yes.
		if allowsActionOn(sc, "", "", "obj", objectOpRights(nodeType)...) {
			t.Errorf("objectOps[%v] offers Rename/Delete to a principal holding nothing", nodeType)
		}
	}
}

// The rights must also be the ones the matching New-X item names, or a login
// permitted to create an object of the type is refused the item that deletes
// it (and the reverse — the pair reads as arbitrary).
func TestServerScopedOpRightsMatchTheNewItemRights(t *testing.T) {
	for _, tc := range []struct {
		node  NodeType
		right requiredRight
	}{
		{NodeLogin, rightAlterAnyLogin},
		{NodeServerRole, rightAlterAnyServerRole},
		{NodeCredential, rightAlterAnyCredential},
		{NodeAudit, rightAlterAnyAudit},
		{NodeServerAuditSpecification, rightAlterAnyAudit},
		{NodeBackupDevice, rightDiskAdmin},
		{NodeEndpoint, rightAlterAnyEndpoint},
	} {
		rights := objectOpRights(tc.node)
		if len(rights) != 1 || rights[0].name != tc.right.name {
			t.Errorf("objectOpRights(%v) = %v, want [%s]", tc.node, rights, tc.right.name)
		}
	}
	for _, n := range []NodeType{NodeAgentJob, NodeAgentSchedule, NodeAgentAlert, NodeAgentOperator} {
		got, want := rightNames(objectOpRights(n)), rightNames(agentWriteRights())
		if !slices.Equal(got, want) {
			t.Errorf("objectOpRights(%v) = %v, want %v", n, got, want)
		}
	}
}

func rightNames(rights []requiredRight) []string {
	names := make([]string, len(rights))
	for i, r := range rights {
		names[i] = r.name
	}
	return names
}

// columnDeniedConn is objectProbedConn with a DENY on named columns as well —
// "schema.object.column" each.
func columnDeniedConn(t *testing.T, dbName string, objGranted, colDenied []string) *db.ServerConn {
	t.Helper()
	sc, _ := newFakeConn(t, withDeniedColumns(capabilityResponsesWithObjects(true,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"},
		[]string{"Sales", "dbo"}, objGranted, nil), colDenied...)...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), dbName)
	return sc
}

// TestADenyOnAColumnWithholdsTheObjectOps. A column-scope DENY beats every
// wider grant exactly as an object-scope one does — the table's own grant of
// ALTER included — so an action touching the whole object must be withheld,
// and the reason it names must be the column rather than a right the login
// already holds.
//
// Only a table-wide action is gated this way: nothing gossms writes is scoped
// to named columns, so there is no case where the denied column could be left
// out of the statement.
func TestADenyOnAColumnWithholdsTheObjectOps(t *testing.T) {
	sc := columnDeniedConn(t, "appdb", []string{"Sales.Orders"}, []string{"Sales.Orders.SSN"})

	if allowsActionOn(sc, "appdb", "Sales", "Orders", rightAlterOnObject) {
		t.Error("a column denial did not withhold the action — the table's grant answered for it")
	}
	r, col, denied := deniedOnObject(sc, "appdb", "Sales", "Orders", rightAlterOnObject)
	if !denied || col != "SSN" {
		t.Fatalf("deniedOnObject = %q, %q, %v; want ALTER, \"SSN\", true", r.name, col, denied)
	}
	if got, want := deniedText(r, col), "ALTER is denied on column SSN of this object."; got != want {
		t.Errorf("deniedText = %q, want %q", got, want)
	}
	// The object's own sentence is unchanged where the DENY is on the object.
	if got, want := deniedText(r, ""), "ALTER is denied on this object."; got != want {
		t.Errorf("deniedText for an object denial = %q, want %q", got, want)
	}
	// And the denial stays on the object it was recorded for: a sibling table
	// keeps whatever the wider scopes gave it.
	if _, _, denied := deniedOnObject(sc, "appdb", "Sales", "Customers", rightAlterOnObject); denied {
		t.Error("a column denial on one table read as a denial on another")
	}
}
