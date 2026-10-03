package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// evictInventory must only drop the entry the finishing load actually
// belongs to. The sys-schema loader used to delete unconditionally, so this
// sequence stranded a live load: disconnect (purgeCompletionInventories
// drops the entry) → reconnect to the same server (a new entry goes in under
// the same key, with its own fetch in flight) → the old connection's fetch
// finally fails and evicts the *new* entry. Its load then completed into an
// object no longer in the map, leaving sys completion unavailable until some
// later lookup started a third load.
func TestEvictInventoryOnlyDropsItsOwnEntry(t *testing.T) {
	stale := &completionInventory{}
	live := &completionInventory{}
	const key = "srv,1433,,sa"

	m := map[string]*completionInventory{key: live}

	evictInventory(m, key, stale)
	if m[key] != live {
		t.Fatal("a stale load's eviction removed the newer entry installed after a reconnect")
	}

	evictInventory(m, key, live)
	if _, ok := m[key]; ok {
		t.Error("the current entry's own eviction did not remove it, so the next lookup won't retry")
	}
}

// Eviction of a key that isn't there at all (both entries already purged)
// must be a no-op rather than panicking or inserting anything.
func TestEvictInventoryMissingKeyIsNoOp(t *testing.T) {
	m := map[string]*completionInventory{}
	evictInventory(m, "gone", &completionInventory{})
	if len(m) != 0 {
		t.Errorf("evicting a missing key left the map with %d entries, want 0", len(m))
	}
}

// A disconnect must supersede every load it purges, not merely stop it. The
// purge deletes the entry, so a fetch that completed just before the
// disconnect has nowhere to land — but Cancel leaves seq untouched, so its
// callback still passed Done, applied the catalog to an entry no longer in the
// map, and called setStatus, replacing "Disconnected" on the status bar with
// "Autocomplete ready for <db>". Abandon is the one that supersedes; see
// latest, and ARCHITECTURE.md § Latest-only loads.
//
// Mutation check: put either Abandon back to Cancel and the matching subtest
// fails.
func TestPurgeCompletionInventoriesAbandonsInFlightLoads(t *testing.T) {
	opts := config.Connection{Server: "srv", User: "sa"}
	sc := &db.ServerConn{Opts: opts}
	serverKey := sysCompletionInventoryKey(opts)

	perDB := &completionInventory{loading: true, serverKey: serverKey}
	sys := &completionInventory{loading: true, serverKey: serverKey}

	// Each entry has a fetch in flight, exactly as loadCompletionInventory
	// leaves it: a token the callback will hand back to Done.
	_, perDBToken := perDB.load.Begin(context.Background())
	_, sysToken := sys.load.Begin(context.Background())

	a := newTestApp()
	a.completionInventories = map[string]*completionInventory{
		completionInventoryKey(opts, "AdventureWorks"): perDB,
	}
	a.sysCompletionInventories = map[string]*completionInventory{serverKey: sys}

	a.purgeCompletionInventories(sc)

	if len(a.completionInventories) != 0 || len(a.sysCompletionInventories) != 0 {
		t.Fatalf("purge left %d per-database and %d sys entries, want 0 and 0",
			len(a.completionInventories), len(a.sysCompletionInventories))
	}

	if perDB.load.Done(perDBToken) {
		t.Error("a purged per-database load is still current, so its result would apply a catalog to a dropped entry and overwrite the Disconnected status")
	}
	if sys.load.Done(sysToken) {
		t.Error("a purged sys-schema load is still current, so its result would apply a catalog to a dropped entry and overwrite the Disconnected status")
	}
}

// T9: two identities on one server — Windows and Entra Default, both with an
// empty User — get their own catalogs and saved OE filters, and disconnecting
// one leaves the other's alone. They shared one key when it was built from
// User: the second connection was served a catalog filtered by the first's
// metadata visibility, and a disconnect purged both.
func TestTwoIdentitiesOnOneServerKeepTheirOwnCaches(t *testing.T) {
	win := &db.ServerConn{Opts: config.Connection{Server: "srv", AuthMethod: config.AuthWindows}}
	entra := &db.ServerConn{Opts: config.Connection{Server: "srv", AuthMethod: config.AuthEntraDefault}}
	if sysCompletionInventoryKey(win.Opts) == sysCompletionInventoryKey(entra.Opts) {
		t.Error("Windows and Entra Default on one server share a completion key")
	}

	a := newTestApp()
	a.completionInventories = map[string]*completionInventory{}
	a.sysCompletionInventories = map[string]*completionInventory{}
	for _, sc := range []*db.ServerConn{win, entra} {
		k := sysCompletionInventoryKey(sc.Opts)
		a.completionInventories[completionInventoryKey(sc.Opts, "appdb")] = &completionInventory{serverKey: k}
		a.sysCompletionInventories[k] = &completionInventory{serverKey: k}
	}
	folder := nodeData{Type: NodeTables, DBName: "appdb"}
	a.rememberFilter(entra, folder, nameFilter("cust"))

	a.purgeCompletionInventories(win)

	if _, ok := a.sysCompletionInventories[sysCompletionInventoryKey(entra.Opts)]; !ok {
		t.Error("disconnecting the Windows login purged the Entra login's sys catalog")
	}
	if _, ok := a.completionInventories[completionInventoryKey(entra.Opts, "appdb")]; !ok {
		t.Error("disconnecting the Windows login purged the Entra login's appdb catalog")
	}
	if len(a.completionInventories) != 1 || len(a.sysCompletionInventories) != 1 {
		t.Errorf("after the purge: %d per-database and %d sys entries, want 1 and 1 (the Entra login's)",
			len(a.completionInventories), len(a.sysCompletionInventories))
	}

	winTables := []*explorerNode{{data: folder}}
	a.restoreFilters(win, winTables)
	if winTables[0].data.Filter != nil {
		t.Error("the Entra login's saved filter was restored on the Windows login's Tables folder")
	}
	entraTables := []*explorerNode{{data: folder}}
	a.restoreFilters(entra, entraTables)
	if entraTables[0].data.Filter == nil {
		t.Error("the Entra login's saved filter was not restored on its own Tables folder")
	}
}

// openPlaceholderPopup hosts qp, points it at a database whose inventory is
// still loading, and types a FROM-clause prefix so its popup opens on the
// "Loading suggestions..." row. It returns a pointer to the items the
// provider answered last, which the editor keeps no public view of.
func openPlaceholderPopup(t *testing.T, qp *QueryPanel, database string) *[]controls.CompletionItem {
	t.Helper()
	qp.app.panels.AddPanel(qp)
	qp.database = database
	qp.app.completionInventories[completionInventoryKey(qp.conn.Opts, database)] = &completionInventory{
		loading: true, serverKey: sysCompletionInventoryKey(qp.conn.Opts),
	}
	last := new([]controls.CompletionItem)
	qp.editor.SetCompletionProvider(func(req controls.CompletionRequest) ([]controls.CompletionItem, int) {
		items, from := qp.sqlCompletionCandidates(req)
		*last = items
		return items, from
	})
	for _, r := range "SELECT * FROM C" {
		qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	if !qp.editor.CompletionActive() || len(*last) != 1 || !(*last)[0].Placeholder {
		t.Fatalf("setup: popup open = %v, items = %+v; want the placeholder alone", qp.editor.CompletionActive(), *last)
	}
	return last
}

// B12: a run's USE moves the panel to another database while its popup waits
// on the old one's load. When that load lands, the popup must be re-asked and
// fill from the panel's new database. The refresh used to match panels by
// server+database, so the moved panel matched neither and the placeholder
// stayed until Escape.
//
// Mutation check: make forServerQueryPanels match on
// completionInventoryKey(qp.conn.Opts, qp.database), the old per-database
// rule, and this fails.
func TestInventoryLoadRefreshesPopupAfterUSEMovedDatabase(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "tempdb", testCustomersOrders())
	last := openPlaceholderPopup(t, qp, "master")

	qp.database = "tempdb" // the run's USE, via setResult
	masterKey := completionInventoryKey(qp.conn.Opts, "master")
	qp.app.completionInventories[masterKey].applyCatalog(&gosmo.Catalog{}, "")
	qp.app.refreshSysCompletionPopups(qp.app.completionInventories[masterKey].serverKey)

	if !qp.editor.CompletionActive() {
		t.Fatal("popup closed; want it filled from tempdb's inventory")
	}
	if got := labels(*last); !slices.Contains(got, "dbo.Customers") {
		t.Errorf("items = %v (%+v), want tempdb's tables in place of the placeholder", got, *last)
	}
}

// Running is never a completion gesture: every run entry point closes the
// popup before anything else, refused runs included, so a placeholder can't
// outlive the keystroke that opened it.
func TestRunEntryPointsCloseCompletion(t *testing.T) {
	for name, run := range map[string]func(*QueryPanel){
		"Execute":           (*QueryPanel).Execute,
		"ExecuteSelection":  (*QueryPanel).ExecuteSelection,
		"ShowEstimatedPlan": (*QueryPanel).ShowEstimatedPlan,
	} {
		t.Run(name, func(t *testing.T) {
			qp := newTestQueryPanelWithInventory(t, "tempdb", testCustomersOrders())
			openPlaceholderPopup(t, qp, "master")
			qp.executing = true // refuse the run: the fake connection has no session

			run(qp)
			if qp.editor.CompletionActive() {
				t.Fatalf("%s left the completion popup open", name)
			}
			// The load landing after the run must not reopen it...
			qp.app.completionInventories[completionInventoryKey(qp.conn.Opts, "master")].applyCatalog(&gosmo.Catalog{}, "")
			qp.app.refreshSysCompletionPopups(sysCompletionInventoryKey(qp.conn.Opts))
			if qp.editor.CompletionActive() {
				t.Errorf("a refresh after %s reopened the popup", name)
			}
			// ...but it is closed, not Escape-dismissed: typing on in the
			// same token reopens it (in tempdb, which has a Customers).
			qp.database = "tempdb"
			qp.editor.HandleKey(tcell.NewEventKey(tcell.KeyRune, "u", tcell.ModNone))
			if !qp.editor.CompletionActive() {
				t.Errorf("typing after %s didn't reopen the popup — the token was suppressed", name)
			}
		})
	}
}

// A load that panics evicts its entry and must not leave a popup on the
// placeholder it would have replaced. It closes rather than refreshes: a
// refresh would start a fresh load that panics the same way, in a loop.
func TestLoadPanickedClosesWaitingPopup(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "tempdb", testCustomersOrders())
	openPlaceholderPopup(t, qp, "master")
	key := completionInventoryKey(qp.conn.Opts, "master")
	inv := qp.app.completionInventories[key]
	_, seq := inv.load.Begin(context.Background())

	m := qp.app.completionInventories
	qp.app.completionLoadPanicked(&inv.load, seq, inv.serverKey, func() bool { return m[key] == inv }, func() { evictInventory(m, key, inv) })
	if _, ok := qp.app.completionInventories[key]; ok {
		t.Error("the panicked load's entry is still cached")
	}
	if qp.editor.CompletionActive() {
		t.Error("popup still open on the placeholder after its load panicked")
	}
}
