package tui

import (
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// The External Resources folder — SSMS's home for the PolyBase and Machine
// Learning Services objects, a sibling of Views rather than anything under
// Programmability. Three folders and three leaf loaders, kept in their own
// file for that reason.

// loadExternalResourcesChildren returns the folder's three families.
//
// External Libraries is omitted outright on SQL Server 2016 and older, which
// has no sys.external_libraries at all: gosmo refuses that read with
// ErrUnsupportedVersion, and a folder whose only possible answer is an error
// is worse than no folder. An unread version (0, which includes Azure — see
// serverMajor) is treated as newest and gets the folder.
func loadExternalResourcesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbName := node.data.DBName
	out := []*explorerNode{
		l.node("External Data Sources", NodeExternalDataSources, "", "", dbName),
		l.node("External File Formats", NodeExternalFileFormats, "", "", dbName),
	}
	if major := serverMajor(l.sc); major == 0 || major >= int(gosmo.SQLServer2017) {
		out = append(out, l.node("External Libraries", NodeExternalLibraries, "", "", dbName))
	}
	return out, nil
}

func loadExternalDataSourcesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.ExternalDataSource, error) { return dbObj.ExternalDataSourcesContext(l.ctx) },
		func(s *gosmo.ExternalDataSource) *explorerNode {
			return l.node(s.Name, NodeExternalDataSource, "", s.Name, node.data.DBName)
		})
}

func loadExternalFileFormatsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.ExternalFileFormat, error) { return dbObj.ExternalFileFormatsContext(l.ctx) },
		func(f *gosmo.ExternalFileFormat) *explorerNode {
			return l.node(f.Name, NodeExternalFileFormat, "", f.Name, node.data.DBName)
		})
}

// loadExternalLibrariesChildren lists the R/Python packages registered with
// CREATE EXTERNAL LIBRARY. The label carries the language, which is the one
// thing separating two libraries of the same name in different runtimes.
func loadExternalLibrariesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	dbObj, err := l.sc.Server.DatabaseByNameContext(l.ctx, node.data.DBName)
	if err != nil {
		return nil, err
	}
	return listChildren(
		func() ([]*gosmo.ExternalLibrary, error) { return dbObj.ExternalLibrariesContext(l.ctx) },
		func(lib *gosmo.ExternalLibrary) *explorerNode {
			label := lib.Name
			if lib.Language != "" {
				label += " (" + lib.Language + ")"
			}
			return l.node(label, NodeExternalLibrary, "", lib.Name, node.data.DBName)
		})
}

// The context menus for this family's nodes, looked up through nodeMenus
// (explorer_loaders.go).

func externalDataSourceMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showExternalDataSourcePropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func externalFileFormatMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showExternalFileFormatPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}

func externalLibraryMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return propertiesOnlyMenu(newQuery, refresh, func() {
		a.showExternalLibraryPropertiesFor(sc, node.data.DBName, node.data.Name)
	})
}
