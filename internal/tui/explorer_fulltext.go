package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_fulltext.go is a database's three full-text folders under
// Storage — Full Text Catalogs, Full Text Stoplists, Search Property Lists —
// and the table menu's Full-Text index cascade. Every leaf opens its
// Properties (fulltext_props.go); Script as and Delete are spliced in from
// scripting.go and explorer_object_ops.go, gated in explorer_object_rights.go.
// The cascade's actions are fulltext_index_ops.go (W18); the folders' New
// items and Define open the new_fulltext_*_dialog.go dialogs (W19).

// fullTextNotInstalledLabel is Storage's one row in place of the three
// folders on an instance without the Full-Text Search component.
const fullTextNotInstalledLabel = "Full-Text Search is not installed"

// fullTextStorageFolders returns the full-text folders Storage lists before
// and after its partition folders, as SSMS orders them: Full Text Catalogs
// first, Full Text Stoplists and Search Property Lists last.
//
// Where FULLTEXTSERVICEPROPERTY says the component is not installed, the
// three folders give way to one row saying so (as SSMS hides them): the
// catalog views still exist there and read empty, which would show three
// folders with nothing that could ever go in them. A failed read keeps the
// folders — fail open; each folder's own read says what went wrong.
func fullTextStorageFolders(l loaderCtx, dbName string) (first, last []*explorerNode) {
	if l.sc != nil && l.sc.Server != nil {
		if info, err := l.sc.Server.FullTextInfo(l.ctx); err == nil && !info.Installed {
			return nil, []*explorerNode{l.node(fullTextNotInstalledLabel, NodeError, "", "", dbName)}
		}
	}
	return []*explorerNode{l.node("Full Text Catalogs", NodeFullTextCatalogs, "", "", dbName)},
		[]*explorerNode{
			l.node("Full Text Stoplists", NodeFullTextStoplists, "", "", dbName),
			l.node("Search Property Lists", NodeSearchPropertyLists, "", "", dbName),
		}
}

// loadFullTextCatalogsChildren lists a database's full-text catalogs by name,
// as SSMS labels them; which one is the default is the Details pane's and
// Properties' to say.
func loadFullTextCatalogsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).FullTextCatalogs,
		func(c *gosmo.FullTextCatalog) *explorerNode {
			return l.node(c.Name, NodeFullTextCatalog, "", c.Name, node.data.DBName)
		})
}

// loadFullTextStoplistsChildren lists a database's user-defined stoplists.
// The system stoplist has no row and no node, as in SSMS.
func loadFullTextStoplistsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).FullTextStoplists,
		func(s *gosmo.FullTextStoplist) *explorerNode {
			return l.node(s.Name, NodeFullTextStoplist, "", s.Name, node.data.DBName)
		})
}

// loadSearchPropertyListsChildren lists a database's search property lists.
func loadSearchPropertyListsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).SearchPropertyLists,
		func(p *gosmo.SearchPropertyList) *explorerNode {
			return l.node(p.Name, NodeSearchPropertyList, "", p.Name, node.data.DBName)
		})
}

// tableFullTextMenu is a table's Full-Text index cascade, in SSMS's order. The
// tree does not know whether the table has an index or what state it is in, so
// nothing here is greyed for that: each action reads the index first and says why
// it does nothing (fulltext_index_ops.go).
//
// Every write is gated on ALTER on the table, which is what each of these
// statements checks — probed on 14 and 17 with a WITHOUT LOGIN user per right:
// ALTER or CONTROL on the table, ALTER on its schema, ALTER ANY SCHEMA, ALTER or
// CONTROL on the database and db_ddladmin each ran ENABLE, DISABLE, SET
// CHANGE_TRACKING, START and STOP POPULATION and DROP; a right on the catalog
// alone (REFERENCES, CONTROL, ALTER ANY FULLTEXT CATALOG) ran none, and none was
// needed beside ALTER on the table.
//
// Define is the exception: CREATE FULLTEXT INDEX also needs REFERENCES on the
// catalog it names, which the menu cannot ask before one is chosen, so the item
// has the cascade's gate and the dialog's preflight asks the rest
// (new_fulltext_index_dialog.go).
func tableFullTextMenu(a *App, sc *db.ServerConn, node *explorerNode) controls.MenuItem {
	op := func(label string, build func() fullTextIndexOp) controls.MenuItem {
		return gate.ItemOn(controls.MenuItem{Label: label, Action: func() { a.runFullTextIndexOp(sc, node, build()) }},
			sc, node.data.DBName, node.data.Schema, node.data.Name, gate.ObjectWriteRights()...)
	}
	track := func(label string, ct gosmo.FullTextChangeTracking) controls.MenuItem {
		return controls.MenuItem{Label: label, Action: func() { a.runFullTextIndexOp(sc, node, fullTextTrackChangesOp(ct)) }}
	}
	return controls.MenuItem{Label: "Full-Text index", Sub: []controls.MenuItem{
		gate.ItemOn(controls.MenuItem{Label: "Define Full-Text Index...", Action: func() { a.showNewFullTextIndexDialog(sc, node) }},
			sc, node.data.DBName, node.data.Schema, node.data.Name, gate.ObjectWriteRights()...),
		{Divider: true},
		op("Enable Full-Text Index", fullTextEnableOp),
		op("Disable Full-Text Index", fullTextDisableOp),
		op("Delete Full-Text Index...", fullTextDeleteOp),
		{Divider: true},
		op("Start Full Population", func() fullTextIndexOp { return fullTextStartOp(gosmo.FullTextPopulationFull) }),
		op("Start Incremental Population", func() fullTextIndexOp { return fullTextStartOp(gosmo.FullTextPopulationIncremental) }),
		op("Stop Population", fullTextStopOp),
		{Divider: true},
		gate.ItemOn(controls.MenuItem{Label: "Track Changes", Sub: []controls.MenuItem{
			track("Manual", gosmo.FullTextChangeTrackingManual),
			track("Automatic", gosmo.FullTextChangeTrackingAuto),
			track("Off", gosmo.FullTextChangeTrackingOff),
		}}, sc, node.data.DBName, node.data.Schema, node.data.Name, gate.ObjectWriteRights()...),
		op("Apply Tracked Changes", fullTextApplyTrackedChangesOp),
		{Divider: true},
		{Label: "Properties...", Action: func() {
			a.showFullTextIndexPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
		}},
	}}
}

// The context menus for this family's folders and leaves, looked up through
// nodeMenus (explorer_loaders.go). Each New item is gated on CREATE FULLTEXT
// CATALOG, the one right all three CREATEs check — it reads 1 under ALTER ANY
// FULLTEXT CATALOG, ALTER/CONTROL on the database and db_ddladmin too. Properties
// is not gated: its pages declare their own rights and come up read-only without
// them; the Delete spliced in beside it is gated (objectOpsMenuItems).

func fullTextCatalogsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Full-Text Catalog...",
			Action: func() { a.showNewFullTextCatalogDialog(sc, node) }},
			sc, node.data.DBName, gate.CreateFullTextCatalog),
	)
}

func fullTextStoplistsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Full-Text Stoplist...",
			Action: func() { a.showNewFullTextStoplistDialog(sc, node) }},
			sc, node.data.DBName, gate.CreateFullTextCatalog),
	)
}

func searchPropertyListsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return folderMenu(newQuery, refresh,
		gate.Item(controls.MenuItem{Label: "New Search Property List...",
			Action: func() { a.showNewSearchPropertyListDialog(sc, node) }},
			sc, node.data.DBName, gate.CreateFullTextCatalog),
	)
}

func fullTextCatalogMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showFullTextCatalogPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func fullTextStoplistMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showFullTextStoplistPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func searchPropertyListMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showSearchPropertyListPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}
