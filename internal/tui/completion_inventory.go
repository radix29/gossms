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
}

// newCompletionInventory builds the lookup indexes for a freshly loaded
// catalog.
func newCompletionInventory(cat *gosmo.Catalog) *completionInventory {
	inv := &completionInventory{
		catalog:         cat,
		byQualifiedName: make(map[string]*gosmo.CatalogObject, len(cat.Objects)),
		bySchema:        make(map[string][]*gosmo.CatalogObject, len(cat.Schemas)),
	}
	for i := range cat.Objects {
		obj := &cat.Objects[i]
		key := strings.ToLower(obj.Schema) + "." + strings.ToLower(obj.Name)
		inv.byQualifiedName[key] = obj
		schemaKey := strings.ToLower(obj.Schema)
		inv.bySchema[schemaKey] = append(inv.bySchema[schemaKey], obj)
	}
	return inv
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
	a.loadCompletionInventory(sc, database, key)
	return inv
}

// refreshCompletionInventory drops any cached entry for sc+database and
// starts a fresh load — Ctrl+R / Query > Refresh IntelliSense Cache.
func (a *App) refreshCompletionInventory(sc *db.ServerConn, database string) {
	key := completionInventoryKey(sc.Opts, database)
	delete(a.completionInventories, key)
	a.ensureCompletionInventory(sc, database)
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
func (a *App) loadCompletionInventory(sc *db.ServerConn, database, key string) {
	srv := sc.Server
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), completionInventoryTimeout)
		defer cancel()
		cat, err := srv.Database(database).CatalogContext(ctx)
		a.postEvent(func() {
			var inv *completionInventory
			if err != nil {
				inv = &completionInventory{err: err}
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for %s: %v", database, err))
			} else {
				inv = newCompletionInventory(cat)
				a.setStatus(fmt.Sprintf("Autocomplete ready for %s (%d tables/views)", database, len(cat.Objects)))
			}
			a.completionInventories[key] = inv
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
	a.loadSysCompletionInventory(sc, key)
	return inv
}

// retrySysCompletionInventory reloads the sys-schema inventory for sc's
// server only if its last load failed — part of Ctrl+R. The sys catalog
// itself never changes while a server is up, so a successful (or still
// loading) snapshot is deliberately kept; this is just the retry path for
// a connect-time failure, which otherwise had none.
func (a *App) retrySysCompletionInventory(sc *db.ServerConn) {
	key := sysCompletionInventoryKey(sc.Opts)
	if inv, ok := a.sysCompletionInventories[key]; ok && inv.err == nil {
		return
	}
	delete(a.sysCompletionInventories, key)
	a.ensureSysCompletionInventory(sc)
}

// loadSysCompletionInventory fetches the "sys" schema catalog on a
// background goroutine and installs the result via postEvent — same shape
// (and same wakeEventLoop-outside-the-closure requirement) as
// loadCompletionInventory. The query runs against "master": any database
// would return the same catalog-view definitions, and master is the one
// every login connecting to a server can reach.
func (a *App) loadSysCompletionInventory(sc *db.ServerConn, key string) {
	srv := sc.Server
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), completionInventoryTimeout)
		defer cancel()
		cat, err := srv.Database("master").SystemCatalogContext(ctx)
		a.postEvent(func() {
			if err != nil {
				a.sysCompletionInventories[key] = &completionInventory{err: err}
				a.setStatus(fmt.Sprintf("System-catalog autocomplete unavailable: %v (Ctrl+R in a query editor retries)", err))
				return
			}
			a.sysCompletionInventories[key] = newCompletionInventory(cat)
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
