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
// the moment a load is kicked off until its postEvent lands; err is set if
// that load failed. byQualifiedName and bySchema are built once, right
// after a successful load, so completion_provider.go's per-keystroke
// lookups never re-scan catalog.Objects.
//
// loadSeq and cancelLoad guard this entry's in-flight background fetch —
// the same beginLoad/endLoad pattern explorerNode uses to guard Object
// Explorer node fetches (see object_explorer.go): loadSeq is bumped on
// every fetch request, so a fetch whose result arrives after a newer one
// was started for the same key can recognize itself as stale and drop
// itself instead of clobbering fresher data; cancelLoad stops that
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
}

// beginLoad cancels whatever fetch is already in flight for this entry (a
// fast double-refresh, or a Refresh before the initial load returned) and
// starts a new timeout-bound one, derived from parent. The caller must
// pass seq to endLoad once the fetch completes, so a stale result can
// recognize itself and refuse to overwrite fresher data.
func (inv *completionInventory) beginLoad(parent context.Context, timeout time.Duration) (ctx context.Context, seq int) {
	if inv.cancelLoad != nil {
		inv.cancelLoad()
	}
	inv.loadSeq++
	ctx, inv.cancelLoad = context.WithTimeout(parent, timeout)
	return ctx, inv.loadSeq
}

// endLoad reports whether seq (as returned by beginLoad) is still current
// — false means a newer beginLoad has since superseded it, and the result
// belonging to seq must be discarded. Clears cancelLoad on success, since
// the fetch it guarded has now finished.
func (inv *completionInventory) endLoad(seq int) bool {
	if inv.loadSeq != seq {
		return false
	}
	inv.cancelLoad = nil
	return true
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
	inv := &completionInventory{loading: true}
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

// refreshCompletionCache is Ctrl+R while the SQL editor has focus, and
// Query > Refresh IntelliSense Cache: drops and reloads this panel's
// completion inventory. A no-op (with a status message) for a panel with
// no connection, matching the rest of the app's context-gated actions —
// see cancelExecutingQuery for the same pattern.
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
// installs the result via postEvent — same shape as connectForQueryPanel
// and runQuery. See wakeEventLoop's doc comment for why the wakeup send
// must happen after postEvent, outside its closure, still on this
// goroutine: Run()'s event loop only drains queued callbacks when it wakes
// for some event on EventQ(), so a wakeup nested inside the very closure
// waiting to be drained would never fire.
//
// inv.beginLoad/endLoad guard against a fast double-refresh (or a refresh
// racing the initial load): if a newer load for the same key starts before
// this one's result lands, this one's postEvent recognizes itself as
// superseded and discards its result instead of clobbering the newer one.
func (a *App) loadCompletionInventory(sc *db.ServerConn, database, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.beginLoad(sc.Context(), completionInventoryTimeout)
	go func() {
		cat, err := srv.Database(database).CatalogContext(ctx)
		a.postEvent(func() {
			if !inv.endLoad(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// This key's cache is shared by every ServerConn that
				// resolves to the same server+login+database (Object
				// Explorer's connection and every query panel's own
				// dedicated one) — sc merely happened to be the one whose
				// ensureCompletionInventory call started this fetch. sc
				// having been closed mid-fetch says nothing about whether
				// some other connection sharing the key is still alive and
				// wants the result, and err's exact shape here depends on a
				// race (a context-cancellation error if sc.Close() ran
				// while the query already held a checked-out connection, or
				// a plain "database is closed" if it ran before one was
				// even acquired — sql.DB.Conn checks its closed flag before
				// context cancellation) — checking sc.IsOpen() directly
				// instead of matching either error shape covers both. Drop
				// the entry instead of poisoning it with sc's own teardown
				// error, so the next lookup (from any connection) just
				// retries fresh.
				delete(a.completionInventories, key)
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
		a.wakeEventLoop()
	}()
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
	inv := &completionInventory{loading: true}
	if a.sysCompletionInventories == nil {
		a.sysCompletionInventories = make(map[string]*completionInventory)
	}
	a.sysCompletionInventories[key] = inv
	a.loadSysCompletionInventory(sc, key, inv)
	return inv
}

// retrySysCompletionInventory reloads the sys-schema inventory for sc's
// server only if its last load failed — part of Ctrl+R. The sys catalog
// itself never changes while a server is up, so a successful (or still
// loading) snapshot is deliberately kept; this is just the retry path for
// a connect-time failure, which otherwise had none. Reuses the existing
// entry (if any) rather than replacing it — see refreshCompletionInventory.
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
// background goroutine and installs the result via postEvent — same shape
// (and same wakeEventLoop-outside-the-closure and beginLoad/endLoad
// stale-result guard) as loadCompletionInventory. The query runs against
// "master": any database would return the same catalog-view definitions,
// and master is the one every login connecting to a server can reach.
func (a *App) loadSysCompletionInventory(sc *db.ServerConn, key string, inv *completionInventory) {
	srv := sc.Server
	ctx, seq := inv.beginLoad(sc.Context(), completionInventoryTimeout)
	go func() {
		cat, err := srv.Database("master").SystemCatalogContext(ctx)
		a.postEvent(func() {
			if !inv.endLoad(seq) {
				return // superseded by a newer load for this key
			}
			if err != nil && !sc.IsOpen() {
				// Same shared-cache reasoning as loadCompletionInventory's
				// equivalent branch, above, but keyed at server level.
				delete(a.sysCompletionInventories, key)
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
		a.wakeEventLoop()
	}()
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
