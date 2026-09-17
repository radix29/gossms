package tui

import (
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// explorer_object_menu.go is the Rename/Delete menu pair itself, and the two
// accessors that name the schema and object a gate question is scoped by.
// The table those answers come from is in explorer_object_ops.go.

// objectOpsMenuItems is the Rename/Delete pair a node offers, or nil.
//
// A system object offers neither, the same way a node type absent from
// objectOps does. The System * folders emit the same node types as the user
// ones, so the type alone put Delete and Rename on master, on sys.objects and
// on the SQL-Server-created Agent jobs: renaming a system database runs
// SET SINGLE_USER WITH ROLLBACK IMMEDIATE — kicking every connection to msdb
// or model — before the server refuses the rename itself, and renaming a
// system Agent job is not refused at all.
func (a *App) objectOpsMenuItems(node *explorerNode) []controls.MenuItem {
	op := objectOpFor(node.data.Type)
	if op == nil || node.data.IsSystem {
		return nil
	}
	sc, dbName := resolveConn(node), node.data.DBName
	groups := objectDataRightGroups(node.data)
	schema, object := objectOpSchema(node), objectOpName(node)

	var items []controls.MenuItem
	if op.rename != nil {
		items = append(items, gate.ItemOnAll(controls.MenuItem{Label: "Rename...",
			Action: func() { a.renameObject(node) }}, sc, dbName, schema, object, groups...))
	}
	if op.transfer != nil {
		// The source half only: the target is picked in the dialog and its
		// ALTER is checked by the server. Gating on the source is what stops
		// the item being offered on an object the login cannot touch at all.
		items = append(items, gate.ItemOn(controls.MenuItem{Label: "Move to Schema...",
			Action: func() { a.moveObjectToSchema(node) }}, sc, dbName, schema, object, objectTransferRights(node.data)...))
	}
	if op.drop != nil || op.dropWithOption != nil {
		items = append(items, gate.ItemOnAll(controls.MenuItem{Label: "Delete...",
			Action: func() { a.deleteObject(node) }}, sc, dbName, schema, object, groups...))
	}
	return items
}

// objectOpSchema is the schema whose ALTER permits these operations on node,
// or "" for a node they are not scoped by.
//
// A schema node is excluded deliberately: ALTER on a schema does not permit
// dropping or renaming the schema itself — that is CONTROL on it, or
// ALTER ANY SCHEMA — so answering with the node's own name would offer three
// items the server then refuses.
func objectOpSchema(n *explorerNode) string { return objectDataSchema(n.data) }

// objectOpName is the object whose own ALTER permits these operations on
// node, or "" for a node they are not scoped by.
//
// A schema node is excluded for the same reason objectOpSchema excludes it:
// the schema is not an object, and its own name would answer for a table that
// happened to share it. A node type gosmo's object probe does not record —
// an index, a statistic — simply has no row, and the wider rights answer as
// they did before.
func objectOpName(n *explorerNode) string { return objectDataObject(n.data) }
