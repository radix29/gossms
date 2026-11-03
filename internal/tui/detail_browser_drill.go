package tui

import (
	"context"
	"strings"

	dbconn "github.com/radix29/gossms/internal/db"
)

// detail_browser_drill.go is SSMS's double-click in Object Explorer Details:
// a row standing for one of the shown node's children selects that child in
// the tree, so the pane navigates into it. A row that stands for no child (a
// Property/Value record, a history or report line) shows its cell's value
// instead, as right-click > Show Value.
//
// The child may not be loaded yet: the pane lists a folder by its own query,
// and the tree loads the folder's children only on its first expand. The
// navigation then expands the folder and waits for that load (drillHint).

// drillHint is a Details double-click waiting for parent's children to load.
type drillHint struct {
	parent *explorerNode
	match  func(*explorerNode) bool
}

// drillIntoRow navigates the tree into the child Details row row stands for,
// and reports whether row stands for one.
func (a *App) drillIntoRow(db *DetailBrowser, row int) bool {
	match := db.drillMatch(row)
	if match == nil {
		return false
	}
	a.explorer.DrillInto(db.currentNode, match)
	return true
}

// drillMatch recognises the tree child Details row row describes: by its
// object's nodeKey where the view has row objects, else by the row's first
// cell against the child's label (a generic folder lists its children by
// label, and Group by Schema's schema rows name their groups). In a
// Property/Value record only a childFolderRows row qualifies; any other names
// a field, never a child. nil when the row describes no child.
func (db *DetailBrowser) drillMatch(row int) func(*explorerNode) bool {
	parent := db.currentNode
	if parent == nil || parent.retired {
		return nil
	}
	cells := db.grid.Row(row)
	if len(cells) == 0 {
		return nil
	}
	label := cells[0]
	if isPropertyValueColumns(db.base.cols) {
		if len(cells) < 2 || cells[1] != detailFolderValue {
			return nil
		}
		// The tree may decorate a folder's label with its state, as the Agent
		// node's " (Stopped)"; the row carries the loader's plain label.
		return func(c *explorerNode) bool {
			return c.label == label || strings.HasPrefix(c.label, label+" (")
		}
	}
	var key *nodeKey
	if db.rowObjsNode == parent && row < len(db.rowObjs) {
		key = new(db.rowObjs[row].key())
	}
	return func(c *explorerNode) bool {
		if key != nil && c.data.key() == *key {
			return true
		}
		return c.label == label
	}
}

// DrillInto selects the child of parent that match picks, expanding parent
// (and the schema group holding the child) on the way. When parent's children
// aren't loaded it starts the load and selects the child once it lands
// (SetChildren); a selection the user makes meanwhile cancels that. No child
// matching leaves the selection on parent.
func (oe *ObjectExplorer) DrillInto(parent *explorerNode, match func(*explorerNode) bool) {
	oe.drill = nil
	if parent.data.Loaded {
		oe.selectDrillChild(parent, match)
		return
	}
	oe.drill = &drillHint{parent: parent, match: match}
	if !parent.expanded {
		oe.handleExpand(parent.id)
	}
	// Already expanded and not loaded: its load is in flight, and SetChildren
	// finishes the drill when it lands.
}

// landDrill finishes a pending DrillInto once n's children are in the tree.
func (oe *ObjectExplorer) landDrill(n *explorerNode) {
	h := oe.drill
	if h == nil || h.parent != n {
		return
	}
	oe.drill = nil
	oe.selectDrillChild(n, h.match)
}

func (oe *ObjectExplorer) selectDrillChild(parent *explorerNode, match func(*explorerNode) bool) {
	c := drillChild(parent, match)
	if c == nil {
		return
	}
	parent.expanded = true
	c.parent.expanded = true // the schema group under a grouped folder
	oe.rebuild()
	oe.view.SelectID(c.id)
}

// drillChild is the first of parent's children match picks, looking inside
// schema groups too: under Group by Schema a folder's objects are its groups'
// children. A group is itself a candidate, for the schema rows a grouped
// folder's Details lists.
func drillChild(parent *explorerNode, match func(*explorerNode) bool) *explorerNode {
	for _, c := range parent.children {
		if c.retired {
			continue
		}
		if match(c) {
			return c
		}
		if c.data.Type == NodeSchemaGroup {
			for _, gc := range c.children {
				if !gc.retired && match(gc) {
					return gc
				}
			}
		}
	}
	return nil
}

// detailFolderValue is the Value cell of a childFolderRows row: what tells a
// child folder from a property in a Property/Value record.
const detailFolderValue = "Folder"

// childFolderRows is node's child folders as Property/Value rows, from the
// tree's own loader so the two panes list the same folders. node is a snapshot
// (this runs off the UI goroutine). A loader error or a note in place of the
// folders (an offline or inaccessible database) lists none: the properties
// below still show.
func childFolderRows(ctx context.Context, sc *dbconn.ServerConn, node *explorerNode) [][]string {
	load, ok := childLoaders[node.data.Type]
	if !ok {
		return nil
	}
	children, err := load(loaderCtx{ctx: ctx, sc: sc}, node)
	if err != nil {
		return nil
	}
	var rows [][]string
	for _, c := range children {
		if c.data.Type != NodeError {
			rows = append(rows, []string{c.label, detailFolderValue})
		}
	}
	return rows
}
