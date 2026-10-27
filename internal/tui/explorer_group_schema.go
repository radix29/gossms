package tui

import (
	"slices"
	"strings"
)

// explorer_group_schema.go is Group by Schema: a groupedFolderTypes folder's
// objects listed under one NodeSchemaGroup per schema instead of flat. It is a
// pure transform of the folder's already-loaded children, run on the UI
// goroutine with no query, so a toggle keeps every expanded subtree: the object
// nodes, their ids and loaded children are the same *explorerNodes, only
// re-parented. The folder keeps the flat list (explorerNode.flat) as the source
// of truth, so ON→OFF→ON is lossless.

// isGroupedObject reports whether c is one of a grouped folder's objects, which
// a schema node takes, rather than a sub-folder (System Tables, …) or an error
// row, which stay first in the folder itself.
func isGroupedObject(c *explorerNode) bool {
	cl := kindOf(c.data.Type).class
	return (cl == kindLeaf || cl == kindExpandable) && c.data.Schema != ""
}

// listingParent is the node whose loader lists n: its parent, or the folder
// above when that parent is a NodeSchemaGroup, which has no loader. The reload
// after a write to n (create, rename, delete, enable) goes there; reloading the
// group itself would drop its objects for good. Direct parent comparisons (the
// Details invalidation in staleDetails) keep using parent.
func (n *explorerNode) listingParent() *explorerNode {
	p := n.parent
	if p != nil && p.data.Type == NodeSchemaGroup {
		return p.parent
	}
	return p
}

// applyGrouping derives folder's children from folder.flat by the
// App.groupBySchema flag. ON: the non-objects first, in their loaded order,
// then one NodeSchemaGroup per schema holding that schema's objects in their
// loaded order, sorted case-insensitively with the server's order breaking ties
// (two schemas differing only in case are two schemas on a case-sensitive
// database). Only schemas with an object get a group, so a filtered folder
// shows only the matching schemas. OFF: flat as loaded.
//
// A group still in the tree is reused, keeping its id and expanded state; one
// rebuilt after a retire (a toggle OFF, a reload) takes the old one's expanded
// state, or a Refresh after a rename would fold away the object just renamed.
// A group left with no objects is retired.
func (oe *ObjectExplorer) applyGrouping(folder *explorerNode) {
	old := folder.groups
	var groups []*explorerNode
	children := make([]*explorerNode, 0, len(folder.flat))
	if oe.app.groupBySchema {
		bySchema := map[string]*explorerNode{}
		for _, c := range folder.flat {
			if !isGroupedObject(c) {
				children = append(children, c)
				continue
			}
			g := bySchema[c.data.Schema]
			if g == nil {
				g = oe.schemaGroup(folder, c.data.Schema, old)
				g.children = nil
				bySchema[c.data.Schema] = g
				groups = append(groups, g)
			}
			c.parent = g
			g.children = append(g.children, c)
		}
		slices.SortStableFunc(groups, func(a, b *explorerNode) int {
			return strings.Compare(strings.ToLower(a.data.Schema), strings.ToLower(b.data.Schema))
		})
		children = append(children, groups...)
	} else {
		for _, c := range folder.flat {
			c.parent = folder
			children = append(children, c)
		}
	}
	for _, g := range old {
		if !g.retired && !slices.Contains(groups, g) {
			oe.retireGroup(g)
		}
	}
	folder.children = children
	if groups != nil {
		folder.groups = groups
	} else {
		// Kept, retired, so a toggle back ON restores which groups were open.
		folder.groups = old
	}
}

// schemaGroup returns folder's group for schema: the live one from old, else a
// new node expanded as old's retired one for that schema was.
func (oe *ObjectExplorer) schemaGroup(folder *explorerNode, schema string, old []*explorerNode) *explorerNode {
	expanded := false
	for _, g := range old {
		if g.data.Schema != schema {
			continue
		}
		if !g.retired {
			return g
		}
		expanded = g.expanded
	}
	g := &explorerNode{
		id:       oe.allocID(),
		label:    schema,
		parent:   folder,
		expanded: expanded,
		data: nodeData{
			Type:    NodeSchemaGroup,
			conn:    folder.data.conn,
			DBName:  folder.data.DBName,
			Schema:  schema,
			GroupOf: folder.data.Type,
			Loaded:  true,
		},
	}
	oe.byID[g.id] = g
	return g
}

// retireGroup takes a group out of the tree, released by the next rebuild like
// any retired node. Only the group: its objects were re-parented, not dropped,
// so unlike dropChildren nothing below it is retired or has its load cancelled.
func (oe *ObjectExplorer) retireGroup(g *explorerNode) {
	g.retired = true
	g.children = nil
	oe.retired = append(oe.retired, g)
}

// regroupAll re-applies grouping to every loaded grouped folder of every root,
// then rebuilds once: the pass behind a Group by Schema toggle.
//
// Toggling ON expands every group above the selection, so the selected object
// (or a node under it, or its "Loading..." row) stays on screen and rebuild's
// SetNodes keeps it selected by id: the objects are the same nodes, only
// re-parented. Without it the selection would fold into a collapsed group and
// reselect would move it to the group. Toggling OFF needs nothing: an object's
// group was open for it to be selected, so its folder is open too.
func (oe *ObjectExplorer) regroupAll() {
	var folders []*explorerNode
	var walk func(*explorerNode)
	walk = func(n *explorerNode) {
		if groupedFolderTypes[n.data.Type] && n.data.Loaded && !n.retired {
			folders = append(folders, n)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range oe.roots {
		walk(r)
	}
	for _, f := range folders {
		oe.applyGrouping(f)
	}
	if oe.app.groupBySchema {
		for cur := oe.selectedOwner(); cur != nil; cur = cur.parent {
			if cur.data.Type == NodeSchemaGroup {
				cur.expanded = true
			}
		}
	}
	oe.rebuild()
}

// selectedOwner is the node the tree's selection stands for: the selected node,
// or the node a selected "Loading..." row belongs to. nil with no selection.
func (oe *ObjectExplorer) selectedOwner() *explorerNode {
	tn := oe.view.SelectedNode()
	if tn == nil {
		return nil
	}
	switch tag := tn.Tag.(type) {
	case *explorerNode:
		return tag
	case loadingRow:
		return tag.owner
	}
	return nil
}

// toggleGroupBySchema flips View > Group by Schema (Alt+G) for every server in
// the tree. Session only, never saved. No query: the loaded folders regroup in
// place (regroupAll), so every expanded subtree and the selection survive.
func (a *App) toggleGroupBySchema() {
	a.groupBySchema = !a.groupBySchema
	a.menuBar.SetMenus(a.buildMenus())
	// Before regroupAll, whose reselect (off a retired group) fetches the
	// folder: every cached grouped folder and group was built in the other mode.
	a.detailBrowser.InvalidateWhere(a, func(n *explorerNode) bool {
		return groupedFolderTypes[n.data.Type] || n.data.Type == NodeSchemaGroup
	})
	a.explorer.regroupAll()
	a.setStatus("Group by Schema: " + toggleStateWord(a.groupBySchema))
}
