package tui

import (
	"context"
	"fmt"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// completionInventoryTimeout bounds one Catalog load, so a hung or very slow
// server can't leave autocomplete stuck on "Loading..." forever.
const completionInventoryTimeout = 30 * time.Second

// completionCaches is App's IntelliSense metadata, shared by every query panel
// on the same server and identity. UI goroutine only, like every other App
// field.
type completionCaches struct {
	// inventories caches one metadata snapshot per server+identity+database
	// (see completionInventoryKey), shared by every query panel on that
	// database.
	inventories map[string]*completionInventory

	// sysInventories caches one "sys" catalog-view snapshot per
	// server+identity (see sysCompletionInventoryKey) — server-scoped, since
	// sys.tables/sys.columns/… are identical in every database. Populated at
	// connect time, not lazily.
	sysInventories map[string]*completionInventory

	// directories caches each server+login's database list, keyed like
	// sysInventories, for resolving the database part of a cross-database
	// name (completion_crossdb.go).
	directories map[string]*completionDirectory

	// linked caches each server+login's linked servers, their databases and
	// the remote catalogs a four-part name has reached, keyed like
	// sysInventories (completion_linked.go).
	linked map[string]*linkedDirectory
}

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

	// collation is the database's catalog collation (databaseCollation —
	// not its data collation, which a contained database does not compare
	// names under), which decides whether two names are the same object: in a case-sensitive database dbo.Orders and dbo.orders
	// are two tables, and lowered keys let the second shadow the first, so
	// "Orders." offered orders' columns. "" (the sys-schema and linked
	// inventories, or a failed read) folds. Matching a typed prefix stays
	// case-insensitive whatever this says — that is a convenience, not
	// identity.
	collation string

	// byQualifiedName indexes catalog.Objects by schema and name (see
	// qualifiedKey), for resolving "schema.table." / "alias." member lookups.
	byQualifiedName *nameMap[*gosmo.CatalogObject]
	// bySchema groups catalog.Objects by schema name, for offering every
	// table/view in a schema after "schema.".
	bySchema *nameMap[[]*gosmo.CatalogObject]
	// fnByQualifiedName indexes catalog.Functions — table-valued functions —
	// the way byQualifiedName indexes tables and views. Kept apart so a
	// function only ever resolves where it is called (sqlparse.FromRef.Call).
	fnByQualifiedName *nameMap[*gosmo.CatalogObject]
	// aggByQualifiedName indexes catalog.Aggregates — user-defined (CLR)
	// aggregates — for typing a PIVOT over one (pivotColumns).
	aggByQualifiedName *nameMap[*gosmo.CatalogAggregate]

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

	// serverKey is the sysCompletionInventoryKey of the server+identity this entry
	// belongs to. Recorded at creation so purgeCompletionInventories can find
	// every entry for a disconnecting connection without splitting the map
	// key.
	serverKey string
}

// inventoryLoad is the completionLoad an inventory cached in m under key
// runs: owned while m still holds inv there, evicted by evictInventory.
func inventoryLoad[T any](m map[string]*completionInventory, key string, inv *completionInventory, timeout time.Duration, what string,
	fetch func(ctx context.Context) (T, error), apply func(v T, err error)) completionLoad[T] {
	return completionLoad[T]{
		what: what, timeout: timeout, load: &inv.load,
		owned: func() bool { return m[key] == inv },
		evict: func() { evictInventory(m, key, inv) },
		fetch: fetch, apply: apply,
	}
}

// evictInventory drops key's entry from m so the next lookup starts a fresh
// load, but only while that entry is still inv.
//
// The identity check makes this safe to call for a load that may be reporting
// on a cache generation that no longer exists:
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

// applyCatalog installs cat, read from a database with collation, and
// rebuilds the lookup indexes in place, clearing loading and err, so a reused
// entry keeps its load identity across reloads.
func (inv *completionInventory) applyCatalog(cat *gosmo.Catalog, collation string) {
	inv.catalog = cat
	inv.collation = collation
	inv.err = nil
	inv.loading = false
	inv.byQualifiedName = newNameMap[*gosmo.CatalogObject](collation)
	inv.bySchema = newNameMap[[]*gosmo.CatalogObject](collation)
	for i := range cat.Objects {
		obj := &cat.Objects[i]
		inv.byQualifiedName.Set(qualifiedKey(obj.Schema, obj.Name), obj)
		list, _ := inv.bySchema.Get(obj.Schema)
		inv.bySchema.Set(obj.Schema, append(list, obj))
	}
	inv.fnByQualifiedName = newNameMap[*gosmo.CatalogObject](collation)
	for i := range cat.Functions {
		fn := &cat.Functions[i]
		inv.fnByQualifiedName.Set(qualifiedKey(fn.Schema, fn.Name), fn)
	}
	inv.aggByQualifiedName = newNameMap[*gosmo.CatalogAggregate](collation)
	for i := range cat.Aggregates {
		agg := &cat.Aggregates[i]
		inv.aggByQualifiedName.Set(qualifiedKey(agg.Schema, agg.Name), agg)
	}
}

// qualifiedKey is byQualifiedName's key for schema.name. Joined with a NUL
// rather than a '.', which a bracketed schema or object name may contain.
func qualifiedKey(schema, name string) string { return schema + "\x00" + name }

// sameName reports whether a and b name the same object in inv's database.
func (inv *completionInventory) sameName(a, b string) bool {
	return gosmo.SameName(inv.collation, a, b)
}

// completionInventoryKey identifies the shared cache entry for a
// server+identity+database: sysCompletionInventoryKey plus the database. The
// identity matters because the catalog is filtered by metadata visibility —
// see config.Connection.IdentityKey.
func completionInventoryKey(opts config.Connection, database string) string {
	return sysCompletionInventoryKey(opts) + "\x00" + database
}

// ensureCompletionInventory returns the current inventory for sc+database,
// possibly still loading or holding an error from the last attempt, starting a
// background load if there is no entry yet.
func (a *App) ensureCompletionInventory(sc *db.ServerConn, database string) *completionInventory {
	key := completionInventoryKey(sc.Opts, database)
	if inv, ok := a.completion.inventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: sysCompletionInventoryKey(sc.Opts)}
	if a.completion.inventories == nil {
		a.completion.inventories = make(map[string]*completionInventory)
	}
	a.completion.inventories[key] = inv
	a.loadCompletionInventory(sc, database, key, inv)
	return inv
}

// refreshCompletionInventory starts a fresh load for sc+database (Ctrl+R, Query
// > Refresh IntelliSense Cache), reusing any existing entry so its latest
// supersedes the in-flight fetch instead of racing it.
func (a *App) refreshCompletionInventory(sc *db.ServerConn, database string) {
	key := completionInventoryKey(sc.Opts, database)
	inv, ok := a.completion.inventories[key]
	if !ok {
		a.ensureCompletionInventory(sc, database)
		return
	}
	inv.loading = true
	a.loadCompletionInventory(sc, database, key, inv)
}

// purgeCompletionInventories drops every cached catalog for sc's server+login
// and stops any load still in flight. Entries are keyed by instance, identity
// and database rather than by *ServerConn, so without this a
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
	// component back out of the map key.
	for key, inv := range a.completion.inventories {
		if inv.serverKey != serverKey {
			continue
		}
		inv.load.Abandon()
		delete(a.completion.inventories, key)
	}
	if inv, ok := a.completion.sysInventories[serverKey]; ok {
		inv.load.Abandon()
		delete(a.completion.sysInventories, serverKey)
	}
	if d, ok := a.completion.directories[serverKey]; ok {
		d.load.Abandon()
		delete(a.completion.directories, serverKey)
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
// installs the result. inv.load guards a fast double-refresh or a refresh
// racing the initial load: a newer load for the same key makes this one
// discard itself.
func (a *App) loadCompletionInventory(sc *db.ServerConn, database, key string, inv *completionInventory) {
	type result struct {
		cat       *gosmo.Catalog
		schema    string
		collation string
	}
	srv := sc.Server
	startCompletionLoad(a, sc, inventoryLoad(a.completion.inventories, key, inv, completionInventoryTimeout,
		"loading the autocomplete catalog",
		func(ctx context.Context) (r result, err error) {
			if inv.gated && !sc.DatabaseCapabilities(ctx, database).Accessible {
				return r, errNoDatabaseAccess
			}
			if r.cat, err = srv.DatabaseRef(database).Catalog(ctx); err != nil {
				return r, err
			}
			// A failure here costs only "db..t" resolving through dbo alone,
			// so it doesn't fail the catalog.
			r.schema, _ = srv.DatabaseRef(database).CallerDefaultSchema(ctx)
			// Nor does this one: without the collation, names fold, which is
			// what every case-insensitive database wants anyway.
			if d, err := srv.DatabaseByName(ctx, database); err == nil {
				r.collation = databaseCollation(d)
			}
			return r, nil
		},
		func(r result, err error) {
			if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for %s: %v", database, err))
				return
			}
			inv.applyCatalog(r.cat, r.collation)
			inv.defaultSchema = r.schema
			a.setStatus(fmt.Sprintf("Autocomplete ready for %s (%d tables/views)", database, len(r.cat.Objects)))
		}))
}

// ---------------------------------------------------------------------------
// sys-schema inventory: one snapshot per server+login, shared by every
// database and query panel connected to that server.
// ---------------------------------------------------------------------------

// sysCompletionInventoryKey identifies the shared "sys" schema cache entry for a
// server+identity (config.Connection.IdentityKey) — completionInventoryKey
// without the database component, since sys.tables/sys.columns/… are identical
// in every database. Also the key of the database and linked-server
// directories and of the saved OE filters.
func sysCompletionInventoryKey(opts config.Connection) string {
	return opts.IdentityKey()
}

// ensureSysCompletionInventory returns the "sys" schema inventory for sc's
// server+login, starting a background load if there is no entry —
// ensureCompletionInventory's contract at server level. Normally loaded well
// before any keystroke needs it, since connectServer and connectForQueryPanel
// both call it as soon as a connection succeeds.
func (a *App) ensureSysCompletionInventory(sc *db.ServerConn) *completionInventory {
	key := sysCompletionInventoryKey(sc.Opts)
	if inv, ok := a.completion.sysInventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: key}
	if a.completion.sysInventories == nil {
		a.completion.sysInventories = make(map[string]*completionInventory)
	}
	a.completion.sysInventories[key] = inv
	a.loadSysCompletionInventory(sc, key, inv)
	return inv
}

// retrySysCompletionInventory reloads the sys-schema inventory for sc's server
// only if its last load failed — part of Ctrl+R, and the retry for a
// connect-time failure. The sys catalog never changes while a server is up, so a
// successful or still-loading snapshot is kept and the entry reused.
func (a *App) retrySysCompletionInventory(sc *db.ServerConn) {
	key := sysCompletionInventoryKey(sc.Opts)
	inv, ok := a.completion.sysInventories[key]
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

// loadSysCompletionInventory fetches the "sys" schema catalog —
// loadCompletionInventory at server level. The query runs against master:
// every database returns the same catalog-view definitions, and master is the
// one every login can reach.
func (a *App) loadSysCompletionInventory(sc *db.ServerConn, key string, inv *completionInventory) {
	srv := sc.Server
	startCompletionLoad(a, sc, inventoryLoad(a.completion.sysInventories, key, inv, completionInventoryTimeout,
		"loading the system autocomplete catalog",
		srv.DatabaseRef("master").SystemCatalog,
		func(cat *gosmo.Catalog, err error) {
			if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("System-catalog autocomplete unavailable: %v (Ctrl+R in a query editor retries)", err))
				return
			}
			inv.applyCatalog(cat, "")
		}))
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
// on key's server — completionLoadPanicked's ending, where a refresh could loop.
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
