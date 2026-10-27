package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_index_actions.go is an index node's maintenance items — Rebuild,
// Reorganize and Disable — which run at once, confirmed, rather than opening a
// script as the Indexes folder's Rebuild All Indexes does.

// indexVerb is one of the three maintenance statements an index node runs.
type indexVerb int

const (
	indexRebuild indexVerb = iota
	indexReorganize
	indexDisable
)

// indexMenuItems is an index's menu. The three verbs are gated on ALTER on the
// table or view the index is on, which is what ALTER INDEX checks: probed on
// majors 13 and 17 with a WITHOUT LOGIN user per case, ALTER or CONTROL on the
// parent, ALTER on its schema, ALTER ANY SCHEMA, db_ddladmin and ALTER or
// CONTROL on the database each permitted REBUILD, REORGANIZE and DISABLE on a
// table's and a view's index, while no right, SELECT, ALTER on the other
// object, and ALTER on the parent beside DENY ALTER on its schema were refused
// (Msg 1088): gate.ObjectWriteRights() on the parent.
//
// A disabled index offers Rebuild only, as "Rebuild (enables it)": REORGANIZE
// on one fails (Msg 1973) and disabling it again does nothing. A hypothetical
// index has no data and a hash index is on a memory-optimized table, which
// refuses ALTER INDEX outright, so neither offers any of the three.
func indexMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	menu := []controls.MenuItem{newQuery, {Divider: true}}
	if !node.data.IsHypothetical && node.data.IndexType != gosmo.IndexTypeNonClusteredHash {
		gated := func(label string, verb indexVerb) controls.MenuItem {
			return gate.ItemOn(controls.MenuItem{Label: label, Action: func() { a.maintainIndex(sc, node, verb) }},
				sc, node.data.DBName, node.data.Schema, node.data.TableName, gate.ObjectWriteRights()...)
		}
		if node.data.IsEnabled {
			menu = append(menu,
				gated("Rebuild...", indexRebuild),
				gated("Reorganize...", indexReorganize),
				gated("Disable...", indexDisable),
				controls.MenuItem{Divider: true})
		} else {
			menu = append(menu, gated("Rebuild (enables it)...", indexRebuild), controls.MenuItem{Divider: true})
		}
	}
	return append(menu, refresh, controls.MenuItem{Label: "Properties...", Action: func() {
		a.showIndexPropertiesFor(sc, node.data.DBName, node.data.Schema, node.data.TableName, node.data.Name, node.data.OnView)
	}})
}

// indexClustered is whether t is a clustered index, rowstore or columnstore:
// the one whose Disable takes the data it holds offline.
func indexClustered(t gosmo.IndexType) bool {
	return t == gosmo.IndexTypeClustered || t == gosmo.IndexTypeClusteredColumnStore
}

// indexVerbPrompt is verb's confirmation title and text for the index in d.
// What each says was checked live on major 17: disabling a table's clustered
// index disabled its nonclustered indexes, the foreign keys referencing it and
// the indexed views on the table, made every query fail (Msg 8655), and a
// rebuild of it re-enabled it alone; disabling a view's clustered index
// disabled the view's other indexes and left the view readable from its base
// table.
func indexVerbPrompt(d nodeData, verb indexVerb) (title, prompt string) {
	name := gosmo.QuoteName(d.Name)
	parent := gosmo.QualifiedName(d.Schema, d.TableName)
	switch verb {
	case indexReorganize:
		return "Reorganize Index", fmt.Sprintf("Reorganize index %s on %s? It runs online, compacting and reordering the leaf pages.", name, parent)
	case indexDisable:
		switch {
		case indexClustered(d.IndexType) && d.OnView:
			return "Disable Index", fmt.Sprintf("Disable clustered index %s on %s? Its materialized data is deleted and the view's other indexes are disabled with it; the view is read from its base tables until the index is rebuilt.", name, parent)
		case indexClustered(d.IndexType):
			return "Disable Index", fmt.Sprintf("Disable clustered index %s on %s? That takes %s offline: no query or write reaches it until the index is rebuilt. Its other indexes, the foreign keys referencing it and the indexed views on it are disabled too, and a rebuild of this index leaves them disabled.", name, parent, parent)
		}
		return "Disable Index", fmt.Sprintf("Disable index %s on %s? Its data is deleted and queries stop using it until it is rebuilt.", name, parent)
	}
	lock := "writes to"
	if indexClustered(d.IndexType) {
		lock = "every read and write of"
	}
	if !d.IsEnabled {
		return "Rebuild Index", fmt.Sprintf("Rebuild disabled index %s on %s? That enables it. It is rebuilt offline, blocking %s %s until it finishes.", name, parent, lock, parent)
	}
	return "Rebuild Index", fmt.Sprintf("Rebuild index %s on %s? It is rebuilt offline, blocking %s %s until it finishes.", name, parent, lock, parent)
}

// maintainIndex confirms verb, runs it on node's index, and refreshes the
// Indexes folder: a rebuild or disable changes the state the labels show, and
// disabling a clustered index changes its siblings' too.
//
// The index is read by name rather than addressed by a handle, so gosmo's
// refusals that depend on what the index is (hypothetical, memory-optimized)
// answer before a statement is sent.
func (a *App) maintainIndex(sc *db.ServerConn, node *explorerNode, verb indexVerb) {
	if !a.requireConn(sc) {
		return
	}
	d := node.data
	title, prompt := indexVerbPrompt(d, verb)
	display := gosmo.QuoteName(d.Name)
	doing, done := "Rebuilding", "rebuilt"
	switch verb {
	case indexReorganize:
		doing, done = "Reorganizing", "reorganized"
	case indexDisable:
		doing, done = "Disabling", "disabled"
	}
	a.confirmDialog.ShowConfirm(title, prompt, func(confirmed bool) {
		if !confirmed {
			return
		}
		a.runWithProgress(progressJob{
			title:   title,
			message: fmt.Sprintf("%s index %s...", doing, display),
			what:    "maintaining an index",
			sc:      sc,
		}, func(ctx context.Context, _ progressReport) error {
			idx, err := findIndex(ctx, sc, d.DBName, d.Schema, d.TableName, d.Name)
			if err != nil {
				return err
			}
			switch verb {
			case indexReorganize:
				return idx.Reorganize(ctx)
			case indexDisable:
				return idx.Disable(ctx)
			}
			return idx.Rebuild(ctx, gosmo.IndexRebuildOptions{})
		}, func(err error, cancelled bool) {
			switch {
			case cancelled:
				// Re-read rather than assumed: the cancel may have arrived
				// after the statement finished.
				a.setStatus(fmt.Sprintf("%s index %s cancelled", doing, display))
				a.explorer.ReloadFolders(sc, sameNodeAs(node.listingParent()))
			case err != nil:
				a.setStatus(fmt.Sprintf("Failed: %s index %s: %v", doing, display, err))
			default:
				a.explorer.ReloadFolders(sc, sameNodeAs(node.listingParent()))
				a.detailBrowser.Invalidate(a, node)
				a.setStatus(fmt.Sprintf("Index %s on %s %s", display, gosmo.QualifiedName(d.Schema, d.TableName), done))
			}
		})
	})
}
