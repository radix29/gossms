package tui

import (
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_storage.go backs a database's Storage folder — the partition
// functions and schemes SSMS files there. The full-text folders filed there
// too are explorer_fulltext.go's.

// loadStorageChildren returns the Storage folder's own subfolders, in SSMS's
// order: Full Text Catalogs first, the partition folders, then Full Text
// Stoplists and Search Property Lists — or, in place of the three full-text
// folders, the one row saying the component is not installed
// (fullTextStorageFolders).
func loadStorageChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbName := node.data.DBName
	first, last := fullTextStorageFolders(l, dbName)
	out := append(first,
		l.node("Partition Functions", NodePartitionFunctions, "", "", dbName),
		l.node("Partition Schemes", NodePartitionSchemes, "", "", dbName),
	)
	return append(out, last...), nil
}

// loadPartitionFunctionsChildren lists a database's partition functions,
// labelled the way SSMS does: the name plus its boundary count.
func loadPartitionFunctionsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).PartitionFunctions,
		func(pf *gosmo.PartitionFunction) *explorerNode {
			label := fmt.Sprintf("%s (%s, %d boundaries)", pf.Name, pf.InputType, pf.BoundaryCount)
			return l.node(label, NodePartitionFunction, "", pf.Name, node.data.DBName)
		})
}

// loadPartitionSchemesChildren lists a database's partition schemes, each
// labelled with the function it partitions by.
func loadPartitionSchemesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listDatabaseChildren(l, node, (*gosmo.Database).PartitionSchemes,
		func(ps *gosmo.PartitionScheme) *explorerNode {
			return l.node(fmt.Sprintf("%s (%s)", ps.Name, ps.FunctionName), NodePartitionScheme, "", ps.Name, node.data.DBName)
		})
}

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go).

func partitionFunctionMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showPartitionFunctionPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func partitionSchemeMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showPartitionSchemePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}
