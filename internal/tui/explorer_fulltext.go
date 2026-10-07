package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_fulltext.go is a database's three full-text folders under
// Storage — Full Text Catalogs, Full Text Stoplists, Search Property Lists —
// and the table menu's Full-Text index cascade. Read-only for now
// (docs/phase5-plan.md § 27, W15): every leaf opens its Properties
// (fulltext_props.go); create, alter, drop and population control come with
// W16–W19.

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

// tableFullTextMenu is a table's Full-Text index cascade. W15 has only
// Properties, which says so when the table has no full-text index; W18 adds
// enable/disable, population and change tracking here.
func tableFullTextMenu(a *App, sc *db.ServerConn, node *explorerNode) controls.MenuItem {
	return controls.MenuItem{Label: "Full-Text index", Sub: []controls.MenuItem{
		{Label: "Properties...", Action: func() {
			a.showFullTextIndexPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.Name)
		}},
	}}
}

// The context menus for this family's leaves, looked up through nodeMenus
// (explorer_loaders.go). Nothing here writes yet, so nothing is gated.

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
