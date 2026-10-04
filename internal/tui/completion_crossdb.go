package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// Cross-database names: "OtherDb.dbo.Orders", "OtherDb..Orders", "OtherDb."
//
// A name's database part is looked up in the server's database directory before
// anything is loaded, so a typo, an unresolved alias or a database that isn't
// ONLINE costs no query. A database the directory lists gets its own
// completionInventory (the shared entry a query panel connected to it uses),
// loaded on first use and only after HAS_DBACCESS says the login can open it
// (see loadCompletionInventory's gate).
// ---------------------------------------------------------------------------

// completionDirectory is one server+login's database list, keyed by name under
// the server's collation: enough to tell a database name from anything else in
// a qualifier chain, and to skip one that can't be opened. On a case-sensitive
// server `Sales` and `sales` are two databases, and a lowered key let one
// shadow the other. A failed load leaves byName empty until Ctrl+R; the failure
// goes to the status bar, not the entry.
type completionDirectory struct {
	loading bool
	byName  *nameMap[directoryEntry]
	load    latest
}

// directoryEntry is one database as sys.databases lists it.
type directoryEntry struct {
	name  string // as the server spells it — the inventory key uses this
	state string // state_desc; only ONLINE is ever loaded
}

// errNoDatabaseAccess is a cross-database inventory's error when HAS_DBACCESS
// answers no: the catalog read would only fail on its USE.
var errNoDatabaseAccess = errors.New("the login cannot open this database")

// ensureCompletionDirectory returns sc's server database directory, starting
// a background load if there is no entry. Primed at connect time alongside
// the sys-schema inventory, so it is normally ready before a chain needs it.
func (a *App) ensureCompletionDirectory(sc *db.ServerConn) *completionDirectory {
	key := sysCompletionInventoryKey(sc.Opts)
	if d, ok := a.completion.directories[key]; ok {
		return d
	}
	d := &completionDirectory{loading: true}
	if a.completion.directories == nil {
		a.completion.directories = make(map[string]*completionDirectory)
	}
	a.completion.directories[key] = d
	a.loadCompletionDirectory(sc, key, d)
	return d
}

// refreshCompletionDirectory reloads sc's directory (Ctrl+R), so a database
// created since connecting becomes reachable.
func (a *App) refreshCompletionDirectory(sc *db.ServerConn) {
	key := sysCompletionInventoryKey(sc.Opts)
	d, ok := a.completion.directories[key]
	if !ok {
		a.ensureCompletionDirectory(sc)
		return
	}
	d.loading = true
	a.loadCompletionDirectory(sc, key, d)
}

// loadCompletionDirectory reads sys.databases off the UI goroutine.
func (a *App) loadCompletionDirectory(sc *db.ServerConn, key string, d *completionDirectory) {
	startCompletionLoad(a, sc, completionLoad[[]*gosmo.Database]{
		what: "loading the autocomplete database list", timeout: completionInventoryTimeout, load: &d.load,
		owned: func() bool { return a.completion.directories[key] == d },
		evict: func() { delete(a.completion.directories, key) },
		fetch: sc.Server.Databases,
		apply: func(dbs []*gosmo.Database, err error) {
			d.loading = false
			if err != nil {
				a.setStatus(fmt.Sprintf("Autocomplete database list unavailable: %v (Ctrl+R in a query editor retries)", err))
				return
			}
			d.byName = newNameMap[directoryEntry](serverCollation(sc))
			for _, x := range dbs {
				d.byName.Set(x.Name, directoryEntry{name: x.Name, state: x.State})
			}
		},
	})
}

// refreshCrossDatabaseInventories reloads, for Ctrl+R, every gated inventory
// on sc's server — the databases panels reached only by name.
func (a *App) refreshCrossDatabaseInventories(sc *db.ServerConn) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	for key, inv := range a.completion.inventories {
		if inv.gated && inv.serverKey == serverKey {
			inv.loading = true
			a.loadCompletionInventory(sc, inv.database, key, inv)
		}
	}
}

// databaseInventory resolves a database part of a name to its loaded inventory:
// own when name is p's own database, otherwise the other database's, starting
// its load if needed. pending reports the answer is still on its way (the
// directory or that inventory is loading) so the caller can show the loading
// row; refreshSysCompletionPopups re-asks once it lands. A name the directory
// doesn't list, a database that isn't ONLINE, or one the login can't open gives
// nil and not pending.
func (p *QueryPanel) databaseInventory(own *completionInventory, name string) (inv *completionInventory, pending bool) {
	if name == "" {
		return nil, false
	}
	if gosmo.SameName(serverCollation(p.conn), name, p.database) {
		return own, false
	}
	dir := p.app.ensureCompletionDirectory(p.conn)
	if dir.loading {
		return nil, true
	}
	entry, ok := dir.byName.Get(name)
	if !ok || entry.state != "ONLINE" {
		return nil, false
	}
	other := p.app.ensureCrossDatabaseInventory(p.conn, entry.name)
	if other.loading {
		return nil, true
	}
	if other.err != nil || other.catalog == nil {
		return nil, false
	}
	return other, false
}

// ensureCrossDatabaseInventory is ensureCompletionInventory for a database the
// panel only names: the load is gated on HAS_DBACCESS first. An entry already
// there — a panel connected to that database made it — is reused as is.
func (a *App) ensureCrossDatabaseInventory(sc *db.ServerConn, database string) *completionInventory {
	key := completionInventoryKey(sc.Opts, database)
	if inv, ok := a.completion.inventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: sysCompletionInventoryKey(sc.Opts), gated: true, database: database}
	if a.completion.inventories == nil {
		a.completion.inventories = make(map[string]*completionInventory)
	}
	a.completion.inventories[key] = inv
	a.loadCompletionInventory(sc, database, key, inv)
	return inv
}

// chainCandidates answers a qualifier chain of two or more parts (the cursor
// after "a.b." or "a.b.c."):
//
//   - [schema object]: that object's columns in the panel's own database
//     ("dbo.Orders."), tried first since it is what the name means there;
//   - [database schema]: the tables and views of that schema in that database
//     ("Sales.dbo.", "Sales.." for dbo);
//   - [database schema object]: that object's columns ("Sales.dbo.Orders.").
//
// A chain whose first part names no database here, and every four-part chain,
// is then tried as a linked server's (linkedChainCandidates). A database that
// can't be read answers nothing. wait reports a load still in flight.
func (p *QueryPanel) chainCandidates(inv, sysInv *completionInventory, chain []string, prefix string) (items []controls.CompletionItem, wait bool) {
	switch len(chain) {
	case 2:
		if chain[1] != "" {
			if obj := findCatalogObject(inv, sysInv, chain[0], chain[1]); obj != nil {
				return p.columnItemsFor(obj.Columns, prefix), false
			}
		}
		other, pending := p.databaseInventory(inv, chain[0])
		if other == nil {
			return p.orLinked(chain, prefix, pending)
		}
		// "Sales.." offers both schemas it can mean, the default one's
		// objects winning a name both hold, as they do on the server.
		var objs []*gosmo.CatalogObject
		seen := newNameSet(other.collation)
		for _, schema := range other.qualifierSchemas(chain[1]) {
			list, ok := other.bySchema.Get(schema)
			if !ok && sysInv != nil {
				list, _ = sysInv.bySchema.Get(schema)
			}
			for _, obj := range list {
				if !seen.Has(obj.Name) {
					seen.Add(obj.Name)
					objs = append(objs, obj)
				}
			}
		}
		if len(objs) == 0 {
			return nil, false
		}
		return p.objectItems(objs, prefix), false
	case 3:
		other, pending := p.databaseInventory(inv, chain[0])
		if other == nil {
			return p.orLinked(chain, prefix, pending)
		}
		for _, schema := range other.qualifierSchemas(chain[1]) {
			if obj := findCatalogObject(other, sysInv, schema, chain[2]); obj != nil {
				return p.columnItemsFor(obj.Columns, prefix), false
			}
		}
		return nil, false
	}
	return p.linkedChainCandidates(chain, prefix)
}

// orLinked is chainCandidates' answer once chain's first part named no
// readable database: the loading row while the database directory or that
// database is still loading, else the linked-server reading.
func (p *QueryPanel) orLinked(chain []string, prefix string, pending bool) ([]controls.CompletionItem, bool) {
	if pending {
		return nil, true
	}
	return p.linkedChainCandidates(chain, prefix)
}

// databaseItems offers every ONLINE database on the panel's server whose name
// contains the lower-cased pl, for the object list: what starts a three-part
// name. Listing them also lets the '.' after one re-sync an open popup, since a
// '.' never opens one from closed.
func (p *QueryPanel) databaseItems(pl string) []controls.CompletionItem {
	dir := p.app.ensureCompletionDirectory(p.conn)
	var items []controls.CompletionItem
	for e := range dir.byName.Values() {
		if e.state != "ONLINE" {
			continue
		}
		if ok, partial := nameMatch(e.name, pl); ok {
			items = append(items, controls.CompletionItem{
				Text: gosmo.QuoteNameIfNeeded(e.name), Label: e.name, Detail: "database",
				Icon: nodeIcon(nodeData{Type: NodeDatabase}, p.app.cfg.IconStyle, false), Partial: partial,
			})
		}
	}
	return items
}

// databaseSchemaItems offers the schemas of another database — "Sales." —
// plus the sys-schema inventory's, which every database carries.
func (p *QueryPanel) databaseSchemaItems(other, sysInv *completionInventory, prefix string) []controls.CompletionItem {
	pl := strings.ToLower(prefix)
	var items []controls.CompletionItem
	for _, in := range []*completionInventory{other, sysInv} {
		if in == nil || in.catalog == nil {
			continue
		}
		for _, schema := range in.catalog.Schemas {
			if ok, partial := nameMatch(schema, pl); ok {
				items = append(items, controls.CompletionItem{
					Text: gosmo.QuoteNameIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
					Partial: partial,
				})
			}
		}
	}
	sortCompletionItems(items)
	return items
}

// qualifierSchemas is the schemas a name's schema part means in inv's
// database, in the order the server tries them: the part itself when given;
// for an empty one ("db..t"), the login's default schema there, then dbo.
func (inv *completionInventory) qualifierSchemas(schema string) []string {
	if schema != "" {
		return []string{schema}
	}
	if inv.defaultSchema == "" || inv.sameName(inv.defaultSchema, "dbo") {
		return []string{"dbo"}
	}
	return []string{inv.defaultSchema, "dbo"}
}
