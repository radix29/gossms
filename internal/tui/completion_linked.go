package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// Linked-server four-part names: "LS.", "LS.Sales.", "LS.Sales.dbo.",
// "LS.Sales.dbo.Orders." and "FROM LS.Sales.dbo.Orders o".
//
// Three lazy loads per server+login, each latest-only and bounded by
// linkedLoadTimeout: the linked servers this one may query (sys.servers, data
// access on), one linked server's databases, and one remote database's catalog
// (gosmo.Server.LinkedServerCatalog — the remote's own catalog views through
// OPENQUERY, so SQL Server remotes only). A slow or down remote shows the loading
// row until the bound, then nothing; its failure stays cached until Ctrl+R, so a
// dead remote is not re-dialled per keystroke.
//
// The local reading of a chain always wins — "X.Y." is tried as schema.object and
// database.schema before server.database — so this file is only asked once
// completion_crossdb.go has found nothing. See docs/decisions.md § IntelliSense:
// linked-server four-part names.
// ---------------------------------------------------------------------------

// linkedLoadTimeout bounds every read through a linked server, and the local
// list of them. Shorter than completionInventoryTimeout: a remote that has
// not answered in this long is one the user is better off typing past.
const linkedLoadTimeout = 10 * time.Second

// linkedDirectory is one server+login's linked servers, keyed by lowercase
// name — only those with data access, the rest failing every read. A failed
// load leaves byName empty until Ctrl+R; the failure goes to the status bar.
type linkedDirectory struct {
	loading bool
	byName  map[string]*linkedServer
	load    latest
}

// linkedServer is one linked server's databases (lowercase name → as the
// remote spells it), loaded on first use, and the catalogs of those of them
// a name has reached, keyed by lowercase database name.
type linkedServer struct {
	name        string
	loading     bool
	err         error
	databases   map[string]string
	load        latest
	inventories map[string]*completionInventory
}

// abandon stops every load under d and drops its results — d is leaving
// App.completion.linked (disconnect, Ctrl+R).
func (d *linkedDirectory) abandon() {
	d.load.Abandon()
	for _, ls := range d.byName {
		ls.load.Abandon()
		for _, inv := range ls.inventories {
			inv.load.Abandon()
		}
	}
}

// purgeLinkedCompletion drops serverKey's linked-server cache, stopping its
// loads, so the next lookup reads everything afresh.
func (a *App) purgeLinkedCompletion(serverKey string) {
	if d, ok := a.completion.linked[serverKey]; ok {
		d.abandon()
		delete(a.completion.linked, serverKey)
	}
}

// ensureLinkedDirectory returns sc's linked-server list, starting its load if
// there is no entry.
func (a *App) ensureLinkedDirectory(sc *db.ServerConn) *linkedDirectory {
	key := sysCompletionInventoryKey(sc.Opts)
	if d, ok := a.completion.linked[key]; ok {
		return d
	}
	d := &linkedDirectory{loading: true}
	if a.completion.linked == nil {
		a.completion.linked = make(map[string]*linkedDirectory)
	}
	a.completion.linked[key] = d
	startCompletionLoad(a, sc, completionLoad[[]*gosmo.LinkedServer]{
		what: "loading the autocomplete linked-server list", timeout: linkedLoadTimeout, load: &d.load,
		owned: func() bool { return a.completion.linked[key] == d },
		evict: func() { delete(a.completion.linked, key) },
		fetch: sc.Server.LinkedServers,
		apply: func(list []*gosmo.LinkedServer, err error) {
			d.loading = false
			if err != nil {
				a.setStatus(fmt.Sprintf("Autocomplete linked-server list unavailable: %v (Ctrl+R in a query editor retries)", err))
				return
			}
			d.byName = make(map[string]*linkedServer, len(list))
			for _, l := range list {
				if l.DataAccess {
					d.byName[strings.ToLower(l.Name)] = &linkedServer{name: l.Name}
				}
			}
		},
	})
	return d
}

// linkedServerNamed resolves name to a linked server on the panel's server.
// pending reports the list still loading; an unknown name, or one without
// data access, gives nil and not pending.
func (p *QueryPanel) linkedServerNamed(name string) (ls *linkedServer, pending bool) {
	if name == "" {
		return nil, false
	}
	d := p.app.ensureLinkedDirectory(p.conn)
	if d.loading {
		return nil, true
	}
	return d.byName[strings.ToLower(name)], false
}

// linkedDatabases resolves name to a linked server whose database list has
// loaded, starting that load on first use.
func (p *QueryPanel) linkedDatabases(name string) (ls *linkedServer, pending bool) {
	ls, pending = p.linkedServerNamed(name)
	if ls == nil {
		return nil, pending
	}
	if ls.databases == nil && ls.err == nil && !ls.loading {
		p.app.loadLinkedDatabases(p.conn, ls)
	}
	if ls.loading {
		return nil, true
	}
	if ls.err != nil {
		return nil, false
	}
	return ls, false
}

// loadLinkedDatabases reads ls's databases through it. ls stays listed
// whatever happens: evicting it means only clearing its latch, so the next
// lookup retries.
func (a *App) loadLinkedDatabases(sc *db.ServerConn, ls *linkedServer) {
	key := sysCompletionInventoryKey(sc.Opts)
	d := a.completion.linked[key]
	srv := sc.Server
	ls.loading = true
	startCompletionLoad(a, sc, completionLoad[[]string]{
		what: "loading a linked server's autocomplete database list", timeout: linkedLoadTimeout, load: &ls.load,
		owned: func() bool { return a.completion.linked[key] == d },
		evict: func() { ls.loading = false },
		fetch: func(ctx context.Context) ([]string, error) { return srv.LinkedServerDatabases(ctx, ls.name) },
		apply: func(names []string, err error) {
			ls.loading = false
			if err != nil {
				ls.err = err
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for linked server %s: %v", ls.name, err))
				return
			}
			ls.databases = make(map[string]string, len(names))
			for _, n := range names {
				ls.databases[strings.ToLower(n)] = n
			}
		},
	})
}

// linkedInventory resolves "server.database" to that remote database's loaded
// catalog, starting its load on first use. A database the linked server does
// not list costs no remote catalog read.
func (p *QueryPanel) linkedInventory(server, database string) (inv *completionInventory, pending bool) {
	if database == "" {
		return nil, false
	}
	ls, pending := p.linkedDatabases(server)
	if ls == nil {
		return nil, pending
	}
	name, ok := ls.databases[strings.ToLower(database)]
	if !ok {
		return nil, false
	}
	k := strings.ToLower(name)
	inv, ok = ls.inventories[k]
	if !ok {
		inv = &completionInventory{loading: true, serverKey: sysCompletionInventoryKey(p.conn.Opts), database: name}
		if ls.inventories == nil {
			ls.inventories = make(map[string]*completionInventory)
		}
		ls.inventories[k] = inv
		p.app.loadLinkedInventory(p.conn, ls, k, inv)
	}
	if inv.loading {
		return nil, true
	}
	if inv.err != nil || inv.catalog == nil {
		return nil, false
	}
	return inv, false
}

// loadLinkedInventory reads one remote database's catalog through ls.
func (a *App) loadLinkedInventory(sc *db.ServerConn, ls *linkedServer, k string, inv *completionInventory) {
	srv := sc.Server
	startCompletionLoad(a, sc, inventoryLoad(ls.inventories, k, inv, linkedLoadTimeout,
		"loading a linked server's autocomplete catalog",
		func(ctx context.Context) (*gosmo.Catalog, error) {
			return srv.LinkedServerCatalog(ctx, ls.name, inv.database)
		},
		func(cat *gosmo.Catalog, err error) {
			if err != nil {
				inv.err = err
				inv.loading = false
				a.setStatus(fmt.Sprintf("Autocomplete unavailable for %s.%s: %v", ls.name, inv.database, err))
				return
			}
			inv.applyCatalog(cat, "")
			a.setStatus(fmt.Sprintf("Autocomplete ready for %s.%s (%d tables/views)", ls.name, inv.database, len(cat.Objects)))
		}))
}

// linkedChainCandidates answers a qualifier chain once its local readings
// have named nothing:
//
//   - [server]: that linked server's databases ("LS.");
//   - [server database]: the remote database's schemas ("LS.Sales.");
//   - [server database schema]: its tables and views ("LS.Sales.dbo.");
//   - [server database schema object]: their columns ("LS.Sales.dbo.Orders.").
//
// An empty part ("LS..", "LS.Sales..") answers nothing: SQL Server refuses
// a four-part name with an omitted database or schema outright (Msg 7313,
// "an invalid schema or catalog was specified for the provider").
func (p *QueryPanel) linkedChainCandidates(chain []string, prefix string) (items []controls.CompletionItem, wait bool) {
	if len(chain) == 1 {
		ls, pending := p.linkedDatabases(chain[0])
		if ls == nil {
			return nil, pending
		}
		return p.linkedDatabaseItems(ls, prefix), false
	}
	if len(chain) > 4 || chain[1] == "" {
		return nil, false
	}
	inv, pending := p.linkedInventory(chain[0], chain[1])
	if inv == nil {
		return nil, pending
	}
	switch len(chain) {
	case 2:
		return p.databaseSchemaItems(inv, nil, prefix), false
	case 3:
		if chain[2] == "" {
			return nil, false
		}
		objs, _ := inv.bySchema.Get(chain[2])
		return p.objectItems(objs, prefix), false
	default:
		if chain[2] == "" {
			return nil, false
		}
		if obj := findCatalogObject(inv, nil, chain[2], chain[3]); obj != nil {
			return p.columnItemsFor(obj.Columns, prefix), false
		}
		return nil, false
	}
}

// linkedDatabaseItems offers ls's databases matching prefix — "LS.".
func (p *QueryPanel) linkedDatabaseItems(ls *linkedServer, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, name := range ls.databases {
		if ok, partial := nameMatch(name, pl); ok {
			items = append(items, controls.CompletionItem{
				Text: gosmo.QuoteNameIfNeeded(name), Label: name, Detail: "database — " + ls.name,
				Icon: nodeIcon(nodeData{Type: NodeDatabase}, p.app.cfg.IconStyle, false), Partial: partial,
			})
		}
	}
	sortCompletionItems(items)
	return items
}

// linkedServerItems offers every queryable linked server whose name contains
// the lower-cased pl, for the object list — what starts a four-part name.
// The list loads on first call and is not waited for: it joins the popup
// when it lands, as databaseItems' does.
func (p *QueryPanel) linkedServerItems(pl string) []controls.CompletionItem {
	d := p.app.ensureLinkedDirectory(p.conn)
	var items []controls.CompletionItem
	for _, ls := range d.byName {
		if ok, partial := nameMatch(ls.name, pl); ok {
			items = append(items, controls.CompletionItem{
				Text: gosmo.QuoteNameIfNeeded(ls.name), Label: ls.name, Detail: "linked server",
				Icon: nodeIcon(nodeData{Type: NodeLinkedServer}, p.app.cfg.IconStyle, false), Partial: partial,
			})
		}
	}
	return items
}

// linkedObject resolves a four-part FROM ref to the remote table or view,
// for resolveRef. An empty database or schema part resolves to nothing (see
// linkedChainCandidates).
func (p *QueryPanel) linkedObject(server, database, schema, name string) (obj *gosmo.CatalogObject, pending bool) {
	if schema == "" {
		return nil, false
	}
	inv, pending := p.linkedInventory(server, database)
	if inv == nil {
		return nil, pending
	}
	return findCatalogObject(inv, nil, schema, name), false
}
