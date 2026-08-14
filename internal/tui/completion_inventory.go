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

// completionInventoryTimeout bounds how long a Catalog load is allowed to
// run — a hung or very slow server shouldn't leave the SQL editor's
// autocomplete stuck "Loading..." forever.
const completionInventoryTimeout = 30 * time.Second

// completionInventory is one database's cached metadata snapshot for SQL
// editor autocomplete (see App.completionInventories). loading is true from
// the moment a load is kicked off until its result lands; err is set if
// that load failed. byQualifiedName and bySchema are built once, right
// after a successful load, so completion_provider.go's per-keystroke
// lookups never re-scan catalog.Objects.
//
// loadSeq and cancelLoad guard this entry's in-flight background fetch —
// the same beginLoad/endLoad pattern explorerNode uses for Object Explorer
// node fetches (see object_explorer.go): loadSeq is bumped on every fetch
// request, so a result arriving after a newer fetch started for the same
// key recognizes itself as stale and drops itself; cancelLoad stops that
// superseded fetch's context outright.
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

	// serverKey is the sysCompletionInventoryKey of the server+login this
	// entry belongs to — the same value for a database-level entry as for
	// the sys-schema one covering the whole server. Recorded at creation so
	// purgeCompletionInventories can find every entry for a disconnecting
	// connection without having to take the map key apart.
	serverKey string
}

// cancel stops this entry's in-flight load, if it has one.
func (inv *completionInventory) cancel() {
	if inv.cancelLoad != nil {
		inv.cancelLoad()
		inv.cancelLoad = nil
	}
}

// beginLoad cancels whatever fetch is already in flight for this entry (a
// fast double-refresh, or a Refresh before the initial load returned) and
// starts a new timeout-bound one, derived from parent. The caller must
// pass seq to endLoad once the fetch completes, so a stale result can
// recognize itself and refuse to overwrite fresher data.
func (inv *completionInventory) beginLoad(parent context.Context, timeout time.Duration) (ctx context.Context, seq int) {
	inv.cancel()
	inv.loadSeq++
	ctx, inv.cancelLoad = context.WithTimeout(parent, timeout)
	return ctx, inv.loadSeq
}

// endLoad reports whether seq (as returned by beginLoad) is still current
// — false means a newer beginLoad has since superseded it, and the result
// belonging to seq must be discarded. Clears cancelLoad on success, since
// the fetch it guarded has now finished — through cancel(), so the timeout
// context is released rather than left registered on its parent with the
// timer still armed. Same reasoning as explorerNode.endLoad.
func (inv *completionInventory) endLoad(seq int) bool {
	if inv.loadSeq != seq {
		return false
	}
	inv.cancel()
	return true
}

// loadPanicked is both loaders' App.safegoRepair step. loading is otherwise
// cleared only in the callback the fetch posts on completion, which a panic
// unwinds straight past — and every later lookup then sees a load in flight
// that no longer exists, so completion never comes back for that key.
//
// The entry is dropped rather than merely unlatched, which is what the
// err-and-closed-connection branch above does for the same reason: the next
// lookup retries from scratch instead of reading a catalog half-built by the
// fetch that died. seq keeps a superseded panic off a live newer load, the
// same way endLoad keeps a superseded result off one.
func loadPanicked(m map[string]*completionInventory, key string, inv *completionInventory, seq int) {
	if !inv.endLoad(seq) {
		return
	}
	evictInventory(m, key, inv)
}

// evictInventory drops key's entry from m so the next lookup starts a fresh
// load — but only while that entry is still inv.
//
// The identity check is what makes this safe to call from a load's own
// completion callback, which by then may be reporting on a cache generation
// that no longer exists: purgeCompletionInventories drops a server's entries
// on disconnect, so a reconnect to the same server before a superseded load
// lands has already installed a different, live entry under the same key.
// Deleting that one would strand its own in-flight load — the load completes
// into an entry nobody can reach, so completion stays unavailable until a
// later lookup starts yet another load. inv's own loadSeq can't catch this:
// it's per-entry, and the stale result belongs to the entry that was
// discarded, whose seq nothing has bumped.
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

// applyCatalog installs cat and rebuilds the lookup indexes on an existing
// entry, in place, clearing loading/err — used by loadCompletionInventory/
// loadSysCompletionInventory so a reused entry keeps its loadSeq/cancelLoad
// identity across reloads instead of being replaced wholesale.
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
// server+login+database — reuses config.ConnectionName's own
// server/port/database/user tuple (see its doc comment for the same
// AuthMethod caveat: Windows Auth and Entra Default against the same
// server/db collide onto one entry, which is harmless here since they'd
// see the same catalog anyway).
func completionInventoryKey(opts config.Connection, database string) string {
	return config.ConnectionName(opts.Server, opts.Port, database, opts.User)
}

// ensureCompletionInventory returns the current inventory for sc+database —
// possibly still loading, possibly holding a stale error from the last
// attempt — starting a fresh background load if there isn't an entry yet.
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

// refreshCompletionInventory starts a fresh load for sc+database — Ctrl+R /
// Query > Refresh IntelliSense Cache. Reuses the existing entry (if any)
// rather than replacing it, so beginLoad can supersede/cancel whatever
// fetch is already in flight for it instead of racing a brand-new one
// against it.
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

// purgeCompletionInventories drops every cached catalog belonging to sc's
// server+login, cancelling any load still in flight for one — called by
// App.disconnect. The entries are keyed by server/port/database/user
// rather than by *ServerConn, so without this a reconnect to the same
// server would be served the catalog captured before the disconnect.
func (a *App) purgeCompletionInventories(sc *db.ServerConn) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	// Matched on the entry's own serverKey rather than by picking the
	// database component back out of the map key: config.ConnectionName
	// joins its four parts with commas, and a server address can itself
	// carry one ("host,1435" — see db.resolveServer), so the key isn't
	// safely splittable.
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

// refreshCompletionCache is Ctrl+R while the SQL editor has focus, and
// Query > Refresh IntelliSense Cache: drops and reloads this panel's
// completion inventory. A no-op (with a status message) for a panel with
// no connection, like the rest of the app's context-gated actions.
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
// installs the result via postAndWake — same shape as connectForQueryPanel
// and runQuery.
//
// inv.beginLoad/endLoad guard against a fast double-refresh (or a refresh
// racing the initial load): if a newer load for the same key starts before
// this one's result lands, this one's callback recognizes itself as
// superseded and discards its result instead of clobbering the newer one.
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
				// This key's cache is shared by every ServerConn that
				// resolves to the same server+login+database (Object
				// Explorer's connection and every query panel's own
				// dedicated one); sc merely happened to be the one whose
				// ensureCompletionInventory call started this fetch. Its
				// having closed mid-fetch says nothing about whether
				// another connection sharing the key still wants the
				// result, and err's exact shape depends on a race — a
				// context-cancellation error if sc.Close() ran while the
				// query already held a checked-out connection, a plain
				// "database is closed" if it ran before one was acquired —
				// so sc.IsOpen() is checked instead of matching either
				// shape. The entry is dropped rather than poisoned with
				// sc's own teardown error, so the next lookup from any
				// connection retries fresh.
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

// refreshCompletionPopups re-queries the completion provider of every
// query panel currently connected to key's server+database, so a load that
// lands while one of them is showing the "Loading suggestions..."
// placeholder fills in live instead of waiting for the next keystroke.
// Editor.RefreshCompletion is itself a no-op unless that panel's popup is
// actually open, so this is safe to call unconditionally for every panel.
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
// database and every query panel connected to that server — see
// sysCompletionInventories' doc comment on App.
// ---------------------------------------------------------------------------

// sysCompletionInventoryKey identifies the shared "sys" schema cache entry
// for a server+login — like completionInventoryKey but with no database
// component, since sys.tables/sys.columns/... are the same set of catalog
// views in every database on the server (see gosmo's SystemCatalogContext).
func sysCompletionInventoryKey(opts config.Connection) string {
	return config.ConnectionName(opts.Server, opts.Port, "", opts.User)
}

// ensureSysCompletionInventory returns the current "sys" schema inventory
// for sc's server+login, starting a fresh background load if there isn't an
// entry yet — same contract as ensureCompletionInventory, just keyed at
// server level. Normally already loaded (or loading) well before any query
// panel's first keystroke needs it, since connectServer and
// connectForQueryPanel both call this as soon as a connection succeeds.
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

// retrySysCompletionInventory reloads the sys-schema inventory for sc's
// server only if its last load failed — part of Ctrl+R. The sys catalog
// never changes while a server is up, so a successful (or still loading)
// snapshot is kept; this is the retry path for a connect-time failure.
// Reuses the existing entry (if any) rather than replacing it — see
// refreshCompletionInventory.
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

// loadSysCompletionInventory fetches the "sys" schema catalog on a
// background goroutine and installs the result via postAndWake — same
// shape (and same beginLoad/endLoad stale-result guard) as
// loadCompletionInventory. The query runs against
// "master": any database would return the same catalog-view definitions,
// and master is the one every login connecting to a server can reach.
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
				// Same shared-cache reasoning as loadCompletionInventory's
				// equivalent branch, above, but keyed at server level.
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
// counterpart: re-queries every query panel connected to key's server,
// regardless of which database each one is in.
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
