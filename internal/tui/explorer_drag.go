package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/theme"
)

// isDraggableNode reports whether t is a concrete SQL Server object that can
// be dragged from Object Explorer into a query editor — every node except
// grouping ("folder") nodes, the server root (no name of its own to drop),
// and the synthetic Loading/Error placeholder rows. A Group by Schema node is
// the one folder that drags: it stands for its schema, which has a name.
func isDraggableNode(t NodeType) bool {
	if t == NodeSchemaGroup {
		return true
	}
	if isContainerNode(t) {
		return false
	}
	switch t {
	case NodeServer, NodeLoading, NodeError:
		return false
	}
	return true
}

// explorerDragText returns the quoted T-SQL identifier to insert into a query
// editor when n is dropped there. Tables, views, stored procedures, functions,
// sequences, synonyms, and triggers are addressable as schema.name in T-SQL (e.g.
// "DROP TRIGGER dbo.MyTrigger"), so those get the full two-part name
// (gosmo.QualifiedName); everything else (databases, schemas, columns, keys,
// indexes, statistics, foreign keys, checks, logins, users, roles, agent jobs,
// linked servers) is quoted as a single bare name. For the table-nested detail types (column, key, index, statistic,
// foreign key, check), n.data.Schema holds the *owning table's* schema, not one
// this object is addressed by — qualifying with it would produce a misleading
// (and invalid) two-part name.
func explorerDragText(n *explorerNode) string {
	switch n.data.Type {
	case NodeTable, NodeView, NodeStoredProcedure, NodeFunction, NodeSequence, NodeSynonym, NodeTrigger,
		// The schema-scoped half of the Programmability families: a type, an
		// XML schema collection, a rule and a default are all addressed as
		// schema.name. Assemblies, plan guides and the external resources are
		// database-scoped and fall through to the bare name below, as do the
		// built-in system data types, which have no schema of their own worth
		// quoting into a query.
		NodeUserDefinedDataType, NodeUserDefinedTableType, NodeUserDefinedType,
		NodeXMLSchemaCollection, NodeRule, NodeDefault,
		// The one schema-scoped Service Broker family. The other six are
		// database-scoped and fall through to the bare name, and so does a
		// route, whose name is not a schema object's at all.
		NodeBrokerQueue:
		return gosmo.QualifiedName(n.data.Schema, n.data.Name)
	case NodeSchemaGroup:
		// A group's name is its schema; data.Name is empty.
		return gosmo.QualifiedName("", n.data.Schema)
	default:
		return gosmo.QualifiedName("", n.data.Name)
	}
}

// dropExplorerNode finishes a started drag of n, armed by a Button1 press
// over a draggable Object Explorer node (see handleMouse in app_events.go): if
// the release lands inside the active query panel's editor, insert the node's
// quoted identifier at that screen position. A release back over Object
// Explorer itself is treated as a cancelled drag (silent, like releasing a
// mouse button without moving); a release anywhere else that isn't a valid
// drop target gets a status message explaining why nothing happened.
func (a *App) dropExplorerNode(n *explorerNode, mx, my int) {
	if mx < a.explorerSplit.FirstRect().Right() {
		return
	}
	qp := a.activeQueryPanel()
	if qp == nil {
		a.setStatus("Drop target must be a query window")
		return
	}
	text := explorerDragText(n)
	if !qp.editor.Bounds().Contains(mx, my) {
		a.setStatus("Drop into the query editor to insert " + text)
		return
	}
	qp.editor.SetCursorFromScreen(mx, my)
	qp.editor.Paste(text)
	a.focusPanels()
	a.setStatus(fmt.Sprintf("Inserted %s", text))
}

// drawDragGhost renders the dragged node's quoted identifier next to the
// mouse cursor while a drag is in progress, so it looks like the object's
// text is being picked up and carried toward the drop target.
func (a *App) drawDragGhost(s tcell.Screen, screenW int) {
	if n, ok := a.drag.Payload(); ok && a.drag.Active() {
		a.drag.DrawGhost(s, explorerDragText(n), theme.StyleSelected(), screenW)
	}
}
