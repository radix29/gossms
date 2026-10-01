package tui

import (
	"errors"
	"strings"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// ---------------------------------------------------------------------------
// Cross-database names: "OtherDb.dbo.Orders", "OtherDb..Orders", "OtherDb."
//
// A name's database part is looked up in the server's database directory
// before anything is loaded, so a typo, an alias that didn't resolve or a
// database that isn't ONLINE costs no query. A database the directory does
// list gets its own completionInventory — the same shared entry a query panel
// connected to it uses — loaded on first use and only after HAS_DBACCESS says
// the login can open it (see loadCompletionInventory's gate).
// ---------------------------------------------------------------------------

// completionDirectory is one server+login's database list, keyed by lowercase
// name: enough to tell a database name from anything else in a qualifier
// chain, and to skip one that can't be opened.
type completionDirectory struct {
	loading bool
	err     error
	byName  map[string]directoryEntry
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
	if d, ok := a.completionDirectories[key]; ok {
		return d
	}
	d := &completionDirectory{loading: true}
	if a.completionDirectories == nil {
		a.completionDirectories = make(map[string]*completionDirectory)
	}
	a.completionDirectories[key] = d
	a.loadCompletionDirectory(sc, key, d)
	return d
}

// refreshCompletionDirectory reloads sc's directory (Ctrl+R), so a database
// created since connecting becomes reachable.
func (a *App) refreshCompletionDirectory(sc *db.ServerConn) {
	key := sysCompletionInventoryKey(sc.Opts)
	d, ok := a.completionDirectories[key]
	if !ok {
		a.ensureCompletionDirectory(sc)
		return
	}
	d.loading = true
	a.loadCompletionDirectory(sc, key, d)
}

// loadCompletionDirectory reads sys.databases off the UI goroutine —
// loadCompletionInventory's shape and stale-result guard.
func (a *App) loadCompletionDirectory(sc *db.ServerConn, key string, d *completionDirectory) {
	srv := sc.Server
	ctx, seq := d.load.BeginTimeout(sc.Context(), completionInventoryTimeout)
	a.safegoRepair("loading the autocomplete database list", func() {
		if d.load.Done(seq) && a.completionDirectories[key] == d {
			delete(a.completionDirectories, key)
		}
	}, func() {
		dbs, err := srv.Databases(ctx)
		a.postAndWake(func() {
			if !d.load.Done(seq) {
				return
			}
			if err != nil && !sc.IsOpen() {
				if a.completionDirectories[key] == d {
					delete(a.completionDirectories, key)
				}
				return
			}
			d.loading = false
			d.err = err
			if err == nil {
				d.byName = make(map[string]directoryEntry, len(dbs))
				for _, x := range dbs {
					d.byName[strings.ToLower(x.Name)] = directoryEntry{name: x.Name, state: x.State}
				}
			}
			a.refreshSysCompletionPopups(key)
		})
	})
}

// refreshCrossDatabaseInventories reloads, for Ctrl+R, every gated inventory
// on sc's server — the databases panels reached only by name.
func (a *App) refreshCrossDatabaseInventories(sc *db.ServerConn) {
	serverKey := sysCompletionInventoryKey(sc.Opts)
	for key, inv := range a.completionInventories {
		if inv.gated && inv.serverKey == serverKey {
			inv.loading = true
			a.loadCompletionInventory(sc, inv.database, key, inv)
		}
	}
}

// databaseInventory resolves a database part of a name to its loaded
// inventory: own when name is p's own database, otherwise the other
// database's, starting its load if needed. pending reports that the answer is
// still on its way — the directory or that inventory is loading — so the
// caller can show the loading row rather than nothing; refreshSysCompletionPopups
// re-asks once it lands. A name the directory doesn't list, a database that
// isn't ONLINE, or one the login can't open, gives nil and not pending.
func (p *QueryPanel) databaseInventory(own *completionInventory, name string) (inv *completionInventory, pending bool) {
	if name == "" {
		return nil, false
	}
	if strings.EqualFold(name, p.database) {
		return own, false
	}
	dir := p.app.ensureCompletionDirectory(p.conn)
	if dir.loading {
		return nil, true
	}
	entry, ok := dir.byName[strings.ToLower(name)]
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
	if inv, ok := a.completionInventories[key]; ok {
		return inv
	}
	inv := &completionInventory{loading: true, serverKey: sysCompletionInventoryKey(sc.Opts), gated: true, database: database}
	if a.completionInventories == nil {
		a.completionInventories = make(map[string]*completionInventory)
	}
	a.completionInventories[key] = inv
	a.loadCompletionInventory(sc, database, key, inv)
	return inv
}

// chainCandidates answers a qualifier chain of two or more parts — the
// cursor after "a.b." or "a.b.c.":
//
//   - [schema object]: that object's columns in the panel's own database
//     ("dbo.Orders."), tried first since it is what the name means there;
//   - [database schema]: the tables and views of that schema in that database
//     ("Sales.dbo.", "Sales.." for dbo);
//   - [database schema object]: that object's columns ("Sales.dbo.Orders.").
//
// Anything else — a linked server's four-part name, a database that can't be
// read — answers nothing. wait reports a load still in flight.
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
			return nil, pending
		}
		schema := strings.ToLower(defaultSchema(chain[1]))
		if objs, ok := other.bySchema[schema]; ok {
			return p.objectItems(objs, prefix), false
		}
		if sysInv != nil {
			if objs, ok := sysInv.bySchema[schema]; ok {
				return p.objectItems(objs, prefix), false
			}
		}
		return nil, false
	case 3:
		other, pending := p.databaseInventory(inv, chain[0])
		if other == nil {
			return nil, pending
		}
		if obj := findCatalogObject(other, sysInv, defaultSchema(chain[1]), chain[2]); obj != nil {
			return p.columnItemsFor(obj.Columns, prefix), false
		}
		return nil, false
	}
	return nil, false
}

// databaseItems offers every ONLINE database on the panel's server whose name
// contains the lower-cased pl, for the object list: what starts a three-part
// name. Listing them is also what lets the '.' after one re-sync an open
// popup, since a '.' never opens one from closed.
func (p *QueryPanel) databaseItems(pl string) []controls.CompletionItem {
	dir := p.app.ensureCompletionDirectory(p.conn)
	var items []controls.CompletionItem
	for _, e := range dir.byName {
		if e.state != "ONLINE" {
			continue
		}
		if ok, partial := nameMatch(e.name, pl); ok {
			items = append(items, controls.CompletionItem{
				Text: bracketIfNeeded(e.name), Label: e.name, Detail: "database",
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
					Text: bracketIfNeeded(schema), Label: schema, Detail: "schema", Icon: p.schemaIcon(),
					Partial: partial,
				})
			}
		}
	}
	sortCompletionItems(items)
	return items
}

// defaultSchema is the schema "db..t" means. The login's own default schema in
// that database isn't known without another read, and is dbo for nearly every
// user, so dbo is assumed.
func defaultSchema(schema string) string {
	if schema == "" {
		return "dbo"
	}
	return schema
}
