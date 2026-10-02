package tui

import (
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// completionInventoryTimeout bounds one Catalog load, so a hung or very slow
// server can't leave autocomplete stuck on "Loading..." forever.
const completionInventoryTimeout = 30 * time.Second

// completionInventory is one database's cached metadata snapshot for SQL editor
// autocomplete. loading is true from the moment a load starts until its result
// lands; err is set if it failed. byQualifiedName and bySchema are built once
// after a successful load, so per-keystroke lookups never re-scan
// catalog.Objects.
//
// load guards this entry's in-flight fetch: a result arriving after a newer
// fetch started drops itself, and the superseded fetch is cancelled. See
// latest.
type completionInventory struct {
	loading bool
	err     error

	catalog *gosmo.Catalog

	// byQualifiedName indexes catalog.Objects by lowercase "schema.name",
	// for resolving "schema.table." / "alias." member lookups.
	byQualifiedName map[string]*gosmo.CatalogObject
	// bySchema groups catalog.Objects by lowercase schema name, for
	// offering every table/view in a schema after "schema.".
	bySchema map[string][]*gosmo.CatalogObject
	// fnByQualifiedName indexes catalog.Functions — table-valued functions —
	// the way byQualifiedName indexes tables and views. Kept apart so a
	// function only ever resolves where it is called (sqlparse.FromRef.Call).
	fnByQualifiedName map[string]*gosmo.CatalogObject

	// defaultSchema is the login's default schema in this database, read with
	// the catalog: what "db..t" means first (see qualifierSchemas). Empty when
	// the read failed or the user has none, which leaves dbo alone.
	defaultSchema string

	load latest

	// gated is set on an inventory a query panel only names, in a
	// cross-database chain (completion_crossdb.go): its load asks HAS_DBACCESS
	// first, so a database the login can't open is answered without a USE
	// that would fail.
	gated bool
	// database is the name a gated entry was loaded for, so Ctrl+R can
	// reload it without a panel connected there.
	database string

	// serverKey is the sysCompletionInventoryKey of the server+login this entry
	// belongs to. Recorded at creation so purgeCompletionInventories can find
	// every entry for a disconnecting connection without splitting the map
	// key.
	serverKey string
}

// loadPanicked is both loaders' safegoRepair step. loading is otherwise cleared
// only in the callback the fetch posts on completion, which a panic unwinds
// straight past, leaving every later lookup seeing a load in flight that doesn't
// exist.
//
// The entry is dropped rather than merely unlatched — the same eviction
// loadCompletionInventory's err-and-closed-connection branch makes — so the next
// lookup retries from scratch instead of reading a catalog half-built by the
// fetch that died. seq keeps a superseded panic off a live newer load.
//
// It also closes every open popup on the entry's server, which may be showing
// the placeholder this load would have replaced. Closed rather than refreshed: a refresh re-asks the provider, which finds the key gone and starts
// a fresh load — one that panics the same way, while the popup stays open,
// loops.
func (a *App) loadPanicked(m map[string]*completionInventory, key string, inv *completionInventory, seq int) {
	if !inv.load.Done(seq) {
		return
	}
	evictInventory(m, key, inv)
	a.closeSysCompletionPopups(inv.serverKey)
}

// evictInventory drops key's entry from m so the next lookup starts a fresh
// load, but only while that entry is still inv.
//
// The identity check makes this safe to call from a load's own completion
// callback, which may be reporting on a cache generation that no longer exists:
// purgeCompletionInventories drops a server's entries on disconnect, so a
// reconnect before a superseded load lands has already installed a different
// live entry under the same key. Deleting that one strands its own in-flight
// load. inv's load token can't catch this — it is per-entry, and the stale result
// belongs to the discarded entry, whose seq nothing bumped.
func evictInventory(m map[string]*completionInventory, key string, inv *completionInventory) {
	if m[key] == inv {
		delete(m, key)
	}
}

// applyCatalog installs cat and rebuilds the lookup indexes in place, clearing
// loading and err, so a reused entry keeps its load identity across reloads.
func (inv *completionInventory) applyCatalog(cat *gosmo.Catalog) {
	inv.catalog = cat
	inv.err = nil
	inv.loading = false
	inv.byQualifiedName = make(map[string]*gosmo.CatalogObject, len(cat.Objects))
	inv.bySchema = make(map[string][]*gosmo.CatalogObject, len(cat.Schemas))
	for i := range cat.Objects {
		obj := &cat.Objects[i]
		key := strings.ToLower(obj.Schema) + "." + strings.ToLower(obj.Name)
		inv.byQualifiedName[key] = obj
		schemaKey := strings.ToLower(obj.Schema)
		inv.bySchema[schemaKey] = append(inv.bySchema[schemaKey], obj)
	}
	inv.fnByQualifiedName = make(map[string]*gosmo.CatalogObject, len(cat.Functions))
	for i := range cat.Functions {
		fn := &cat.Functions[i]
		inv.fnByQualifiedName[strings.ToLower(fn.Schema)+"."+strings.ToLower(fn.Name)] = fn
	}
}

// completionInventoryKey identifies the shared cache entry for a
// server+login+database, reusing config.ConnectionName's
// server/port/database/user tuple. It leaves out the auth method, so two
// logins with an empty User (Windows, most Entra methods) share an entry —
// harmless here, since colliding entries see the same catalog.
func completionInventoryKey(opts config.Connection, database string) string {
	return config.ConnectionName(opts.Server, opts.Port, database, opts.User)
}

// ensureCompletionInventory returns the current inventory for sc+database,
// possibly still loading or holding an error from the last attempt, starting a
// background load if there is no entry yet.
func (a *App) ensureCompletionInventory(sc *db.ServerConn, database string) *completionInventory {
	key := completionInventoryKey(sc.Opts, database)
	if inv, ok := a.completionInventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: sysCompletionInventoryKey(sc.Opts)}
	if a.completionInventories == nil {
		a.completionInventories = make(map[string]*completionInventory)
	}
	a.completionInventories[key] = inv
	a.loadCompletionInventory(sc, database, key, inv)
	return inv
}

// refreshCompletionInventory starts a fresh load for sc+database (Ctrl+R, Query
// > Refresh IntelliSense Cache), reusing any existing entry so its latest
// supersedes the in-flight fetch instead of racing it.
func (a *App) refreshCompletionInventory(sc *db.ServerConn, database string) {
	key := completionInventoryKey(sc.Opts, database)
	inv, ok := a.completionInventories[key]
	if !ok {
		a.ensureCompletionInventory(sc, database)
		return
	}
	inv.loading = true
	a.loadCompletionInventory(sc, database, key, inv)
}

// purgeCompletionInventories drops every cached catalog for sc's server+login
// and stops any load still in flight. Entries are keyed by
// server/port/database/user rather than by *ServerConn, so without this a
// reconnect is served the catalog captured before the disconnect.
//
// Abandon, not Cancel: the entry is leaving the map, so a result already on its
// way has nowhere to land. Cancel leaves seq untouched, so a fetch that
// completed just before the disconnect still passed Done and called setStatus,
// painting "Autocomplete ready for <db>" over "Disconnected" a beat after the
// user disconnected. See latest, and ARCHITECTURE.md § Latest-only loads.
func (a *App) purgeCompletionInventories(sc *db.ServerConn) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	// Matched on the entry's serverKey rather than by picking the database
	// component back out of the map key: ConnectionName joins its parts with
	// commas and a server address can carry one ("host,1435").
	for key, inv := range a.completionInventories {
		if inv.serverKey != serverKey {
			continue
		}
		inv.load.Abandon()
		delete(a.completionInventories, key)
	}
	if inv, ok := a.sysCompletionInventories[serverKey]; ok {
		inv.load.Abandon()
		delete(a.sysCompletionInventories, serverKey)
	}
	if d, ok := a.completionDirectories[serverKey]; ok {
		d.load.Abandon()
		delete(a.completionDirectories, serverKey)
	}
	a.purgeLinkedCompletion(serverKey)
	// A query panel keeps its own connection to the server, so its popup may
	// be waiting on a load just abandoned: re-asking starts a fresh one on
	// that panel's connection, or closes the popup if it has none.
	a.refreshSysCompletionPopups(serverKey)
}

// refreshCompletionCache is Ctrl+R with the SQL editor focused, and Query >
// Refresh IntelliSense Cache: reloads this panel's inventory, the server's
// database list, and every other database's inventory the server's panels
// have loaded through a cross-database name, and drops everything read
// through a linked server, so the next four-part name reads it afresh. A no-op with a status message for
// a panel with no connection.
func (p *QueryPanel) refreshCompletionCache() {
	if p.app.cfg.IntelliSenseDisabled {
		p.app.setStatus("IntelliSense is disabled — enable it in Tools > Options")
		return
	}
	if p.conn == nil {
		p.app.setStatus("Not connected — nothing to refresh")
		return
	}
	p.app.refreshCompletionInventory(p.conn, p.database)
	p.app.retrySysCompletionInventory(p.conn)
	p.app.refreshCompletionDirectory(p.conn)
	p.app.refreshCrossDatabaseInventories(p.conn)
	p.app.purgeLinkedCompletion(sysCompletionInventoryKey(p.conn.Opts))
	p.app.setStatus(fmt.Sprintf("Refreshing autocomplete inventory for %s...", p.database))
}

// loadCompletionInventory fetches the catalog on a background goroutine and
// installs the result via postAndWake. inv.load guards a fast
// double-refresh or a refresh racing the initial load: a newer load for the same
// key makes this callback discard itself.
func (a *App) loadCompletionInventory(sc *db.ServerConn, database, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.load.BeginTimeout(sc.Context(), completionInventoryTimeout)
	a.safegoRepair("loading the autocomplete catalog", func() {
		a.loadPanicked(a.completionInventories, key, inv, seq)
	}, func() {
		var cat *gosmo.Catalog
		var schema string
		var err error
		if inv.gated && !sc.DatabaseCapabilities(ctx, database).Accessible {
			err = errNoDatabaseAccess
		} else {
			cat, err = srv.DatabaseRef(database).Catalog(ctx)
			if err == nil {
				// A failure here costs only "db..t" resolving through dbo
				// alone, so it doesn't fail the catalog.
				schema, _ = srv.DatabaseRef(database).CallerDefaultSchema(ctx)
			}
		}
		a.postAndWake(func() {
			if !inv.load.Done(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// This key's cache is shared by every ServerConn resolving to
				// the same server+login+database; sc merely started the fetch,
				// so its closing mid-fetch says nothing about whether another
				// connection still wants the result. err's shape depends on a
				// race — context cancellation or "database is closed", by
				// whether sc.Close() ran before or after a connection was
				// acquired — so sc.IsOpen() is checked instead of matching
				// either. The entry is dropped rather than poisoned with sc's
				// teardown error, so the next lookup retries fresh — and the
				// refresh below is that lookup for a popup another panel
				// holds open on it.
				evictInventory(a.completionInventories, key, inv)
			} else if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for %s: %v", database, err))
			} else {
				inv.applyCatalog(cat)
				inv.defaultSchema = schema
				a.setStatus(fmt.Sprintf("Autocomplete ready for %s (%d tables/views)", database, len(cat.Objects)))
			}
			// Every panel on the server, not only those connected to this
			// database: another may be waiting on it through a cross-database
			// name. Every outcome ends here — a popup left on the placeholder
			// by a path that skipped this never fills (B12).
			a.refreshSysCompletionPopups(inv.serverKey)
		})
	})
}

// ---------------------------------------------------------------------------
// sys-schema inventory: one snapshot per server+login, shared by every
// database and query panel connected to that server.
// ---------------------------------------------------------------------------

// sysCompletionInventoryKey identifies the shared "sys" schema cache entry for a
// server+login — completionInventoryKey without the database component, since
// sys.tables/sys.columns/… are identical in every database.
func sysCompletionInventoryKey(opts config.Connection) string {
	return config.ConnectionName(opts.Server, opts.Port, "", opts.User)
}

// ensureSysCompletionInventory returns the "sys" schema inventory for sc's
// server+login, starting a background load if there is no entry —
// ensureCompletionInventory's contract at server level. Normally loaded well
// before any keystroke needs it, since connectServer and connectForQueryPanel
// both call it as soon as a connection succeeds.
func (a *App) ensureSysCompletionInventory(sc *db.ServerConn) *completionInventory {
	key := sysCompletionInventoryKey(sc.Opts)
	if inv, ok := a.sysCompletionInventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: key}
	if a.sysCompletionInventories == nil {
		a.sysCompletionInventories = make(map[string]*completionInventory)
	}
	a.sysCompletionInventories[key] = inv
	a.loadSysCompletionInventory(sc, key, inv)
	return inv
}

// retrySysCompletionInventory reloads the sys-schema inventory for sc's server
// only if its last load failed — part of Ctrl+R, and the retry for a
// connect-time failure. The sys catalog never changes while a server is up, so a
// successful or still-loading snapshot is kept and the entry reused.
func (a *App) retrySysCompletionInventory(sc *db.ServerConn) {
	key := sysCompletionInventoryKey(sc.Opts)
	inv, ok := a.sysCompletionInventories[key]
	if ok && inv.err == nil {
		return
	}
	if !ok {
		a.ensureSysCompletionInventory(sc)
		return
	}
	inv.loading = true
	a.loadSysCompletionInventory(sc, key, inv)
}

// loadSysCompletionInventory fetches the "sys" schema catalog on a background
// goroutine and installs it via postAndWake — loadCompletionInventory's shape
// and stale-result guard. The query runs against master: every database returns
// the same catalog-view definitions, and master is the one every login can
// reach.
func (a *App) loadSysCompletionInventory(sc *db.ServerConn, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.load.BeginTimeout(sc.Context(), completionInventoryTimeout)
	a.safegoRepair("loading the system autocomplete catalog", func() {
		a.loadPanicked(a.sysCompletionInventories, key, inv, seq)
	}, func() {
		cat, err := srv.DatabaseRef("master").SystemCatalog(ctx)
		a.postAndWake(func() {
			if !inv.load.Done(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// Same shared-cache reasoning as loadCompletionInventory's,
				// keyed at server level.
				evictInventory(a.sysCompletionInventories, key, inv)
				a.refreshSysCompletionPopups(key)
				return
			}
			if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("System-catalog autocomplete unavailable: %v (Ctrl+R in a query editor retries)", err))
				a.refreshSysCompletionPopups(key)
				return
			}
			inv.applyCatalog(cat)
			a.refreshSysCompletionPopups(key)
		})
	})
}

// refreshSysCompletionPopups re-queries the completion provider of every query
// panel on key's server, whatever database it is in, so a load landing while
// one shows the "Loading suggestions..." placeholder fills in live.
// Editor.RefreshCompletion is a no-op unless that panel's popup is open.
//
// Matched by server, not by server+database: a run's USE moves a panel's
// database while its popup waits on the old one's load, and a per-database
// match then never refreshed it (B12).
func (a *App) refreshSysCompletionPopups(key string) {
	a.forServerQueryPanels(key, func(qp *QueryPanel) { qp.editor.RefreshCompletion() })
}

// closeSysCompletionPopups closes the completion popup of every query panel
// on key's server — loadPanicked's ending, where a refresh could loop.
func (a *App) closeSysCompletionPopups(key string) {
	a.forServerQueryPanels(key, func(qp *QueryPanel) { qp.editor.CloseCompletion() })
}

// forServerQueryPanels calls fn for every query panel connected to key's
// server+login.
func (a *App) forServerQueryPanels(key string, fn func(qp *QueryPanel)) {
	for i := 0; i < a.panels.Count(); i++ {
		qp, ok := a.panels.PanelAt(i).(*QueryPanel)
		if !ok || qp.conn == nil {
			continue
		}
		if sysCompletionInventoryKey(qp.conn.Opts) == key {
			fn(qp)
		}
	}
}
