package tui

import (
	"context"
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

// completionInventory is one database's cached metadata snapshot for SQL
// editor autocomplete. loading is true from the moment a load starts until its
// result lands; err is set if it failed. byQualifiedName and bySchema are built
// once after a successful load, so per-keystroke lookups never re-scan
// catalog.Objects.
//
// loadSeq and cancelLoad guard this entry's in-flight fetch — the same
// beginLoad/endLoad pattern explorerNode uses: loadSeq is bumped on every
// request, so a result arriving after a newer fetch started recognizes itself
// as stale and drops itself, and cancelLoad stops the superseded fetch.
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

	loadSeq    int
	cancelLoad context.CancelFunc

	// serverKey is the sysCompletionInventoryKey of the server+login this entry
	// belongs to — the same value for a database-level entry as for the
	// sys-schema one. Recorded at creation so purgeCompletionInventories can
	// find every entry for a disconnecting connection without splitting the
	// map key.
	serverKey string
}

// cancel stops this entry's in-flight load, if it has one.
func (inv *completionInventory) cancel() {
	if inv.cancelLoad != nil {
		inv.cancelLoad()
		inv.cancelLoad = nil
	}
}

// beginLoad cancels any fetch already in flight for this entry — a fast
// double-refresh, or a Refresh before the initial load returned — and starts a
// new timeout-bound one derived from parent. The caller passes seq to endLoad
// on completion, so a stale result can refuse to overwrite fresher data.
func (inv *completionInventory) beginLoad(parent context.Context, timeout time.Duration) (ctx context.Context, seq int) {
	inv.cancel()
	inv.loadSeq++
	ctx, inv.cancelLoad = context.WithTimeout(parent, timeout)
	return ctx, inv.loadSeq
}

// endLoad reports whether seq is still current; false means a newer beginLoad
// superseded it and seq's result must be discarded. Clears cancelLoad on
// success through cancel(), so the timeout context is released rather than
// left registered on its parent with the timer armed.
func (inv *completionInventory) endLoad(seq int) bool {
	if inv.loadSeq != seq {
		return false
	}
	inv.cancel()
	return true
}

// loadPanicked is both loaders' safegoRepair step. loading is otherwise cleared
// only in the callback the fetch posts on completion, which a panic unwinds
// straight past — every later lookup then sees a load in flight that doesn't
// exist, and completion never comes back for that key.
//
// The entry is dropped rather than merely unlatched, as the
// err-and-closed-connection branch above does: the next lookup retries from
// scratch instead of reading a catalog half-built by the fetch that died. seq
// keeps a superseded panic off a live newer load.
func loadPanicked(m map[string]*completionInventory, key string, inv *completionInventory, seq int) {
	if !inv.endLoad(seq) {
		return
	}
	evictInventory(m, key, inv)
}

// evictInventory drops key's entry from m so the next lookup starts a fresh
// load — but only while that entry is still inv.
//
// The identity check makes this safe to call from a load's own completion
// callback, which may be reporting on a cache generation that no longer
// exists: purgeCompletionInventories drops a server's entries on disconnect,
// so a reconnect before a superseded load lands has already installed a
// different, live entry under the same key. Deleting that one strands its own
// in-flight load, which completes into an entry nobody can reach. inv's
// loadSeq can't catch this — it is per-entry, and the stale result belongs to
// the discarded entry, whose seq nothing bumped.
func evictInventory(m map[string]*completionInventory, key string, inv *completionInventory) {
	if m[key] == inv {
		delete(m, key)
	}
}

// newCompletionInventory builds a fresh entry with the lookup indexes for
// a freshly loaded catalog.
func newCompletionInventory(cat *gosmo.Catalog) *completionInventory {
	inv := &completionInventory{}
	inv.applyCatalog(cat)
	return inv
}

// applyCatalog installs cat and rebuilds the lookup indexes in place, clearing
// loading/err, so a reused entry keeps its loadSeq/cancelLoad identity across
// reloads instead of being replaced wholesale.
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
}

// completionInventoryKey identifies the shared cache entry for a
// server+login+database, reusing config.ConnectionName's
// server/port/database/user tuple. Its AuthMethod caveat applies — Windows Auth
// and Entra Default against the same server/db collide onto one entry — which
// is harmless here, since both see the same catalog.
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

// refreshCompletionInventory starts a fresh load for sc+database (Ctrl+R /
// Query > Refresh IntelliSense Cache), reusing any existing entry so beginLoad
// supersedes the in-flight fetch instead of racing a new one against it.
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
// and cancels any load still in flight — called by App.disconnect. Entries are
// keyed by server/port/database/user rather than by *ServerConn, so without
// this a reconnect is served the catalog captured before the disconnect.
func (a *App) purgeCompletionInventories(sc *db.ServerConn) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	// Matched on the entry's serverKey rather than by picking the database
	// component back out of the map key: ConnectionName joins its four parts
	// with commas and a server address can carry one ("host,1435"), so the key
	// isn't safely splittable.
	for key, inv := range a.completionInventories {
		if inv.serverKey != serverKey {
			continue
		}
		inv.cancel()
		delete(a.completionInventories, key)
	}
	if inv, ok := a.sysCompletionInventories[serverKey]; ok {
		inv.cancel()
		delete(a.sysCompletionInventories, serverKey)
	}
}

// refreshCompletionCache is Ctrl+R with the SQL editor focused, and Query >
// Refresh IntelliSense Cache: drops and reloads this panel's inventory. A
// no-op with a status message for a panel with no connection.
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
	p.app.setStatus(fmt.Sprintf("Refreshing autocomplete inventory for %s...", p.database))
}

// loadCompletionInventory fetches the catalog on a background goroutine and
// installs the result via postAndWake.
//
// inv.beginLoad/endLoad guard a fast double-refresh, or a refresh racing the
// initial load: if a newer load for the same key starts before this result
// lands, this callback recognizes itself as superseded and discards it.
func (a *App) loadCompletionInventory(sc *db.ServerConn, database, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.beginLoad(sc.Context(), completionInventoryTimeout)
	a.safegoRepair("loading the autocomplete catalog", func() {
		loadPanicked(a.completionInventories, key, inv, seq)
	}, func() {
		cat, err := srv.Database(database).CatalogContext(ctx)
		a.postAndWake(func() {
			if !inv.endLoad(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// This key's cache is shared by every ServerConn resolving
				// to the same server+login+database; sc merely started this
				// fetch. Its closing mid-fetch says nothing about whether
				// another connection still wants the result, and err's shape
				// depends on a race — context cancellation if sc.Close() ran
				// while the query held a checked-out connection, "database
				// is closed" if it ran before one was acquired — so
				// sc.IsOpen() is checked instead of matching either. The
				// entry is dropped rather than poisoned with sc's teardown
				// error, so the next lookup retries fresh.
				evictInventory(a.completionInventories, key, inv)
				return
			}
			if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for %s: %v", database, err))
			} else {
				inv.applyCatalog(cat)
				a.setStatus(fmt.Sprintf("Autocomplete ready for %s (%d tables/views)", database, len(cat.Objects)))
			}
			a.refreshCompletionPopups(key)
		})
	})
}

// refreshCompletionPopups re-queries the completion provider of every query
// panel connected to key's server+database, so a load landing while one shows
// the "Loading suggestions..." placeholder fills in live rather than waiting
// for the next keystroke. Editor.RefreshCompletion is a no-op unless that
// panel's popup is open, so this is safe to call for every panel.
func (a *App) refreshCompletionPopups(key string) {
	for i := 0; i < a.panels.Count(); i++ {
		qp, ok := a.panels.PanelAt(i).(*QueryPanel)
		if !ok || qp.conn == nil {
			continue
		}
		if completionInventoryKey(qp.conn.Opts, qp.database) == key {
			qp.editor.RefreshCompletion()
		}
	}
}

// ---------------------------------------------------------------------------
// sys-schema inventory: one snapshot per server+login, shared by every
// database and query panel connected to that server.
// ---------------------------------------------------------------------------

// sysCompletionInventoryKey identifies the shared "sys" schema cache entry for
// a server+login — completionInventoryKey with no database component, since
// sys.tables/sys.columns/... are identical in every database on the server.
func sysCompletionInventoryKey(opts config.Connection) string {
	return config.ConnectionName(opts.Server, opts.Port, "", opts.User)
}

// ensureSysCompletionInventory returns the current "sys" schema inventory for
// sc's server+login, starting a background load if there is no entry —
// ensureCompletionInventory's contract, keyed at server level. Normally already
// loaded well before any keystroke needs it, since connectServer and
// connectForQueryPanel both call it as soon as a connection succeeds.
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
// only if its last load failed — part of Ctrl+R, and the retry path for a
// connect-time failure. The sys catalog never changes while a server is up, so
// a successful or still-loading snapshot is kept, and the existing entry is
// reused rather than replaced.
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
// and stale-result guard. The query runs against master: any database returns
// the same catalog-view definitions, and master is the one every login can
// reach.
func (a *App) loadSysCompletionInventory(sc *db.ServerConn, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.beginLoad(sc.Context(), completionInventoryTimeout)
	a.safegoRepair("loading the system autocomplete catalog", func() {
		loadPanicked(a.sysCompletionInventories, key, inv, seq)
	}, func() {
		cat, err := srv.Database("master").SystemCatalogContext(ctx)
		a.postAndWake(func() {
			if !inv.endLoad(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// Same shared-cache reasoning as loadCompletionInventory's,
				// keyed at server level.
				evictInventory(a.sysCompletionInventories, key, inv)
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

// refreshSysCompletionPopups is refreshCompletionPopups' sys-schema
// counterpart: every query panel on key's server, whatever database it is in.
func (a *App) refreshSysCompletionPopups(key string) {
	for i := 0; i < a.panels.Count(); i++ {
		qp, ok := a.panels.PanelAt(i).(*QueryPanel)
		if !ok || qp.conn == nil {
			continue
		}
		if sysCompletionInventoryKey(qp.conn.Opts) == key {
			qp.editor.RefreshCompletion()
		}
	}
}
